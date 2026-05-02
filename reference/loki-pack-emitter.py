#!/usr/bin/env python3
"""
Loki Pack Telemetry MVP emitter v2 — targets the REAL OpenClaw session schema.

Session files: ~/.openclaw/agents/main/sessions/*.jsonl
Schema (observed):
  Event types:
    - "session"         → session.start metric
    - "message"         → assistant/user turns, toolCall blocks
    - "custom"          → custom events; customType=openclaw:prompt-error → error
    - "model_change" / "thinking_level_change" / "compaction" → ignored
  message.role ∈ {user, assistant, tool_result}
  assistant content blocks: type ∈ {text, thinking, toolCall}
  toolCall: {type, id, name, arguments}
  stopReason ∈ {stop, toolUse, aborted, ...}

Privacy: we emit ONLY counts + bounded enum attrs. Never read arguments/content text.
"""
import argparse
import json
import os
import signal
import sys
import time
import uuid
from collections import defaultdict
from pathlib import Path
from typing import Dict, Optional, Tuple, Iterable

OUTCOMES = {"success", "error", "aborted", "timeout"}
ALLOWED_MODEL_FAMILIES = {"anthropic", "openai", "bedrock", "gemini", "openclaw", "unknown"}
ALLOWED_TOOL_CLASSES = {"shell", "file", "http", "system", "aws", "message", "search", "memory", "agent", "other"}
ALLOWED_ERROR_TYPES = {"transient", "permanent", "config", "auth", "quota", "prompt", "unknown"}

DEFAULT_STATE_DIR = Path(os.environ.get("LOKI_PACK_STATE_DIR", "/var/lib/loki-pack-emitter"))
SESSION_DIR = Path(os.environ.get("LOKI_PACK_SESSION_DIR", str(Path.home() / ".openclaw" / "agents" / "main" / "sessions")))
SCAN_INTERVAL = 15
EXPORT_INTERVAL = 30


def eprint(msg: str) -> None:
    print(msg, file=sys.stderr, flush=True)


def derive_model_family(provider: Optional[str], model: Optional[str]) -> str:
    p = (provider or "").lower()
    m = (model or "").lower()
    # provider is the strongest signal — Bedrock wraps anthropic but counts as bedrock
    if p in ("amazon-bedrock", "bedrock"):
        return "bedrock"
    if p == "openclaw":
        return "openclaw"
    if "anthropic" in p or "claude" in m or "anthropic" in m:
        return "anthropic"
    if "openai" in p or "gpt" in m or m.startswith(("o1", "o3", "o4")):
        return "openai"
    if "gemini" in p or "gemini" in m:
        return "gemini"
    return "unknown"


TOOL_CLASS_MAP = {
    "exec": "shell",
    "process": "shell",
    "read": "file",
    "write": "file",
    "edit": "file",
    "message": "message",
    "memory_search": "memory",
    "memory_get": "memory",
    "web_fetch": "http",
    "sessions_spawn": "agent",
    "sessions_send": "agent",
    "sessions_list": "agent",
    "sessions_history": "agent",
    "sessions_yield": "agent",
    "agents_list": "agent",
    "subagents": "agent",
    "canvas": "system",
    "tts": "system",
    "session_status": "system",
    "image": "other",
    "pdf": "other",
}


def derive_tool_class(tool_name: Optional[str]) -> str:
    if not tool_name:
        return "other"
    return TOOL_CLASS_MAP.get(tool_name, "other")


def derive_error_type(custom_type: Optional[str]) -> str:
    s = (custom_type or "").lower()
    if "prompt" in s:
        return "prompt"
    if "auth" in s:
        return "auth"
    if "quota" in s or "rate" in s:
        return "quota"
    if "config" in s:
        return "config"
    if "transient" in s:
        return "transient"
    if "permanent" in s:
        return "permanent"
    return "unknown"


def atomic_write_json(path: Path, payload: Dict[str, int]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    tmp = path.with_suffix(path.suffix + ".tmp")
    with tmp.open("w", encoding="utf-8") as fh:
        json.dump(payload, fh, sort_keys=True)
    os.replace(tmp, path)


class MetricSink:
    def add(self, name: str, attrs: Dict[str, str]) -> None:
        raise NotImplementedError

    def flush(self) -> None:
        pass


class DryRunSink(MetricSink):
    def __init__(self) -> None:
        self.counts = defaultdict(int)

    def add(self, name: str, attrs: Dict[str, str]) -> None:
        self.counts[(name, tuple(sorted(attrs.items())))] += 1

    def report(self) -> None:
        if not self.counts:
            print("no metrics")
            return
        for (name, attrs), count in sorted(self.counts.items()):
            rendered = ", ".join(f"{k}={v}" for k, v in attrs) or "(no attrs)"
            print(f"{name}  count={count}  [{rendered}]")


class OTelSink(MetricSink):
    def __init__(self, state_dir: Path) -> None:
        from opentelemetry.sdk.metrics import MeterProvider
        from opentelemetry.sdk.metrics.export import PeriodicExportingMetricReader
        from opentelemetry.sdk.resources import Resource

        instance_id = load_instance_id(state_dir)
        endpoint = os.environ.get("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:4317")
        bearer = os.environ.get("LOKI_PACK_BEARER_TOKEN", "")
        resource = Resource.create({
            "service.namespace": "loki.pack",
            "service.name": "openclaw",
            "service.version": os.environ.get("LOKI_PACK_VERSION", "mvp-0.2"),
            "service.instance.id": instance_id,
        })

        # Pick the exporter based on scheme. HTTPS + bearer → OTLP/HTTP through the ingest ALB.
        # gRPC stays as the localhost-collector fallback for dev/rollback.
        if endpoint.startswith("https://") or endpoint.startswith("http://") and "/v1/metrics" in endpoint:
            from opentelemetry.exporter.otlp.proto.http.metric_exporter import OTLPMetricExporter
            headers = {}
            if bearer:
                headers["Authorization"] = f"Bearer {bearer}"
            # Skip TLS verify for the MVP self-signed cert. This is explicitly accepted
            # as MVP posture in docs/plans/06-public-alb-collector-ADDENDUM.md and will
            # be fixed in plan 07 with a real cert.
            if endpoint.startswith("https://"):
                import ssl, requests
                from requests.adapters import HTTPAdapter
                class _NoVerify(HTTPAdapter):
                    def send(self, request, **kw):
                        kw["verify"] = False
                        return super().send(request, **kw)
                _orig_init = requests.Session.__init__
                def _new_init(s, *a, **kw):
                    _orig_init(s, *a, **kw)
                    s.mount("https://", _NoVerify())
                    s.verify = False
                requests.Session.__init__ = _new_init
                import urllib3; urllib3.disable_warnings()
            exporter = OTLPMetricExporter(endpoint=endpoint, headers=headers)
        else:
            from opentelemetry.exporter.otlp.proto.grpc.metric_exporter import OTLPMetricExporter
            exporter = OTLPMetricExporter(endpoint=endpoint, insecure=True)
        reader = PeriodicExportingMetricReader(exporter, export_interval_millis=EXPORT_INTERVAL * 1000)
        self.provider = MeterProvider(resource=resource, metric_readers=[reader])
        meter = self.provider.get_meter("loki-pack-emitter")
        self.counters = {
            "pack.session.start":     meter.create_counter("pack.session.start",     unit="{session}"),
            "pack.agent.turn":        meter.create_counter("pack.agent.turn",        unit="{turn}"),
            "pack.tool.call":         meter.create_counter("pack.tool.call",         unit="{call}"),
            "pack.error":             meter.create_counter("pack.error",             unit="{error}"),
            "pack.emitter.heartbeat": meter.create_counter("pack.emitter.heartbeat", unit="{tick}"),
        }

    def add(self, name: str, attrs: Dict[str, str]) -> None:
        c = self.counters.get(name)
        if c is not None:
            c.add(1, attrs)

    def flush(self) -> None:
        self.provider.force_flush(timeout_millis=5000)


def load_instance_id(state_dir: Path) -> str:
    try:
        v = Path("/sys/devices/virtual/dmi/id/product_uuid").read_text(encoding="utf-8").strip()
        if v:
            return v
    except OSError:
        pass
    state_dir.mkdir(parents=True, exist_ok=True)
    path = state_dir / "instance-id"
    if path.exists():
        v = path.read_text(encoding="utf-8").strip()
        if v:
            return v
    v = str(uuid.uuid4())
    tmp = path.with_suffix(".tmp")
    tmp.write_text(v, encoding="utf-8")
    os.replace(tmp, path)
    return v


class App:
    def __init__(self, sink: MetricSink, state_dir: Path, session_dir: Path) -> None:
        self.sink = sink
        self.state_dir = state_dir
        self.session_dir = session_dir
        self.cursor_path = state_dir / "cursor.json"
        self.cursors = self.load_cursors()
        self.stop = False

    def load_cursors(self) -> Dict[str, int]:
        if not self.cursor_path.exists():
            return {}
        try:
            data = json.loads(self.cursor_path.read_text(encoding="utf-8"))
            return {str(k): int(v) for k, v in data.items()}
        except Exception as exc:
            eprint(f"cursor read failed: {exc}")
            return {}

    def save_cursors(self) -> None:
        atomic_write_json(self.cursor_path, self.cursors)

    def scan(self) -> None:
        if not self.session_dir.is_dir():
            eprint(f"session dir missing: {self.session_dir}")
            return
        for path in sorted(self.session_dir.glob("*.jsonl")):
            # Skip checkpoint files (they are forks, not live)
            if ".checkpoint." in path.name:
                continue
            self.scan_file(path)

    def scan_file(self, path: Path) -> None:
        try:
            size = path.stat().st_size
            offset = self.cursors.get(str(path), 0)
            if offset > size:  # truncated/rotated
                offset = 0
            with path.open("rb") as fh:
                fh.seek(offset)
                while True:
                    line_start = fh.tell()
                    raw = fh.readline()
                    if not raw:
                        break
                    if not raw.endswith(b"\n"):
                        # partial line — wait for next scan
                        fh.seek(line_start)
                        break
                    try:
                        ev = json.loads(raw.decode("utf-8", errors="replace"))
                    except Exception as exc:
                        eprint(f"malformed line in {path.name}: {exc}")
                        continue
                    self.handle(ev)
                self.cursors[str(path)] = fh.tell()
        except OSError as exc:
            eprint(f"scan {path}: {exc}")

    def handle(self, ev: dict) -> None:
        t = ev.get("type")
        if t == "session":
            self.sink.add("pack.session.start", {"session.type": "main"})
            return
        if t == "message":
            self.handle_message(ev.get("message") or {})
            return
        if t == "custom":
            self.handle_custom(ev)
            return
        # ignore model_change, thinking_level_change, compaction

    def handle_message(self, m: dict) -> None:
        role = m.get("role")
        if role != "assistant":
            return  # user / tool_result don't count as a pack.agent.turn
        stop_reason = m.get("stopReason") or ""
        if stop_reason == "aborted":
            outcome = "aborted"
        else:
            outcome = "success"
        model_family = derive_model_family(m.get("provider"), m.get("model"))
        self.sink.add("pack.agent.turn", {
            "model.family": model_family if model_family in ALLOWED_MODEL_FAMILIES else "unknown",
            "outcome": outcome,
        })
        # Count tool calls from toolCall blocks
        blocks = m.get("content") or []
        if isinstance(blocks, list):
            for b in blocks:
                if not isinstance(b, dict):
                    continue
                if b.get("type") == "toolCall":
                    name = b.get("name")
                    tc = derive_tool_class(name)
                    if tc not in ALLOWED_TOOL_CLASSES:
                        tc = "other"
                    self.sink.add("pack.tool.call", {"tool.class": tc})

    def handle_custom(self, ev: dict) -> None:
        ct = ev.get("customType") or ""
        if "error" in ct.lower():
            et = derive_error_type(ct)
            if et not in ALLOWED_ERROR_TYPES:
                et = "unknown"
            self.sink.add("pack.error", {"error.type": et})


def run(args: argparse.Namespace) -> int:
    state_dir = Path(args.state_dir)
    session_dir = Path(args.session_dir)
    sink: MetricSink = DryRunSink() if args.dry_run else OTelSink(state_dir)
    app = App(sink, state_dir, session_dir)

    def handle_stop(signum, _frame) -> None:
        app.stop = True
        eprint(f"signal {signum} received, shutting down")

    signal.signal(signal.SIGTERM, handle_stop)
    signal.signal(signal.SIGINT, handle_stop)

    deadline = time.time() + 30 if (args.dry_run and not args.once) else None
    while not app.stop:
        app.scan()
        app.save_cursors()
        # Heartbeat: one metric + one log line per scan cycle so operators can prove the emitter is alive.
        sink.add("pack.emitter.heartbeat", {})
        eprint(f"heartbeat scan_ok files={len(app.cursors)} ts={int(time.time())}")
        if args.once:
            break
        if deadline is not None and time.time() >= deadline:
            break
        for _ in range(SCAN_INTERVAL):
            if app.stop:
                break
            time.sleep(1)

    app.save_cursors()
    sink.flush()
    if args.dry_run and isinstance(sink, DryRunSink):
        sink.report()
    return 0


def build_parser() -> argparse.ArgumentParser:
    p = argparse.ArgumentParser(description="Loki Pack telemetry emitter (real OpenClaw schema)")
    p.add_argument("--dry-run", action="store_true", help="print metrics instead of exporting")
    p.add_argument("--once", action="store_true", help="single scan pass, then exit")
    p.add_argument("--state-dir", default=str(DEFAULT_STATE_DIR))
    p.add_argument("--session-dir", default=str(SESSION_DIR))
    return p


if __name__ == "__main__":
    try:
        raise SystemExit(run(build_parser().parse_args()))
    except Exception as exc:
        eprint(f"fatal: {exc}")
        raise SystemExit(1)
