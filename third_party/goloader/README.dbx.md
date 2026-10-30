# DBX object loader

Vendored from https://github.com/pkujhd/goloader at
`2695cfc5350631cc3becc1409162b12a885471b5` (Apache-2.0; see LICENSE).
This experimental branch accepts stable Go 1.26 releases from 1.26.6 onward.
Host and modules must still use the exact same patch release and host digest. The object reader is built with a
private Go source tree prepared by scripts/prepare-dbmod-toolchain.py.
System Go is never patched. End users need neither Go nor the build tree.

Local changes: relocate interface method slices and their function types when
inspecting an on-disk host image in the packer (`link/registerInExe.go`).
On Mach-O arm64, interface method pointers pass through the existing pointer
mask before rebasing; a child-process regression covers tagged pointers.
The `dbmod_runtime` tag replaces the compiler object-file symbol reader with
small native PE/Mach-O readers and excludes x86 disassembly. The packer records
instruction names in relocation metadata, so far-address call/data relocations
do not require a decoder in the installed host. Build-only symbol maps and
archive/source paths are removed before serialization by DBX's packer.
`link/ld.go` preserves absent funcdata sentinels while adjusting data offsets,
so panic/recover does not mistake missing entries for open-coded defer metadata.
The far x86 CMP trampoline preserves the original byte/word/dword/qword opcode and operands, preventing adjacent global bytes from changing bool comparisons.
DBX keeps numeric literal pools local to the module to avoid out-of-range SIMD
loads on Windows. DBX saves its CLI arguments before clearing os.Args for vendor
global flag parsing.

DBX serializes completed graphs using its bounded DBXMOD2 format with gzip.
Package and symbol construction order is stable; the build repeats all four
modules in separate processes and compares exact bytes for the same inputs.
This does not make Go's internal ABI stable or promise cross-target compatibility.

Native modules execute directly in the single-shot CLI process, once per engine
per CLI lifetime. DBX does not unload modules from a live process. Binary
compatibility is restricted to the exact host digest. Cancellation has a bounded
grace period before returning to the CLI, which renders an error and exits;
library callers must also terminate their process if a vendor ignores Context.

`link/ld.go` returns the original symbol after adding its missing type, so zero-sized globals cannot reset a live type descriptor to the shared zero slot. Dynamic typelinks are validated and sorted by runtime type string before publication, as required by reflect binary search. Regression tests cover this offset corruption and far CMP widths.

Stage 1 also pins Apple Silicon JIT writes to one OS thread and orders package/symbol construction deterministically. Existing zero-size type, CMP-width and type-link fixes are retained.

Weak calls reserve the same trampoline space as ordinary calls. ARM64 branch
relocation masks R_WEAK before choosing ARM versus ARM64 instructions; otherwise
a far weak call emits an ARM32 instruction. Two regressions cover reservation
and the full ARM64 trampoline/target bytes.

Offline prelink resolves only load-base-independent relocations, leaves defer/stack-object/dynamic references for runtime, and never moves code or PC/GC offsets. The builder repeats construction of all four vendor modules and requires exact payload bytes.

The DBXMOD2 codec and conservative prelink implementation were adapted from
Tao2301230/cli-dbx commit a9bde215fad4d353883ebfcb24a4d21b376f006e.
DBX retains relocation instruction names for its smaller Windows runtime.
Relative-offset targets require local copies even when the host has an absolute
function address; a regression covers this distinction.
