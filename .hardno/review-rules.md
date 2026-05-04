## Go Code Quality — Senior Reviewer Standards

### DRY (Don't Repeat Yourself)
- Flag duplicated logic sequences across functions — if two functions share 3+ lines of identical logic (e.g., stage → backup → rename), extract a shared helper
- Flag inline binary path resolution, file existence checks, or version comparisons that duplicate existing package-level helpers
- Flag functions that differ only in a pre/post hook — use a callback/hook pattern instead of copy-paste-modify
- Flag duplicated error handling patterns — extract common cleanup into deferred functions or helper methods

### Single Responsibility Principle
- Flag functions longer than ~50 lines — they likely do more than one thing
- Flag functions that mix I/O, business logic, and state management — separate concerns into layers
- Flag god-methods that handle download + verify + extract + backup + state + rename in one flow — decompose into named steps
- Each file should have a clear, single purpose. Flag files that mix unrelated types or concerns.

### Magic Strings & Constants
- Flag hardcoded paths (e.g., `/usr/local/bin/telemetron`, `/var/lib/telemetron/`) outside of a designated constants file
- Flag hardcoded event names in log statements that aren't defined as constants — event names are part of the monitoring contract
- Flag hardcoded HTTP status codes, timeout durations, or retry counts scattered through business logic — extract to named constants
- Exception: test files may use inline values for clarity

### Modularization & Package Design
- Each package should have a clear public API. Flag packages where internal helpers are exported without need.
- Flag circular dependencies or packages that reach into each other's internals
- Flag types that belong in package A but are defined in package B (e.g., path constants for the updater living in the service package)
- Flag constructors that leave interface fields nil — use no-op implementations to prevent latent panics

### State Management
- Flag in-memory state that diverges from on-disk state after errors — mutations must be reverted on write failure
- Flag state writes in cleanup/error paths that silently discard errors without logging
- Flag state read-modify-write sequences that don't hold a lock or use an atomic update pattern

### Error Handling in Go
- Flag unchecked error returns (`errcheck` violations) — use `_ =` with a comment for intentionally ignored errors
- Flag error paths that leave resources (files, staged binaries, temp dirs) behind — cleanup must happen on all paths
- Flag `os.Exit()` in library code or inside deferred cleanup scopes — prefer returning sentinel errors

### API Surface
- Public methods should be usable without reading the implementation. Flag methods that panic on valid (if unusual) inputs.
- Flag public constructors that produce objects in an invalid state (nil required fields, uninitialized channels)
- Flag inconsistent naming: if one constructor is `New()` and another is `NewForX()`, the naming should clearly convey the difference in lifecycle/ownership

### Comments & Documentation
- Flag stale comments that contradict the code (e.g., "no StateFile" when one is initialized)
- Flag commented-out code — delete it, git has history
- Public types and functions must have godoc comments. Flag missing or trivially obvious ones ("New creates a new X").
