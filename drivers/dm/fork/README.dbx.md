# DBX DM driver patch

Source: `gitee.com/chunanyong/dm v1.8.22`, synchronized with Dameng 8.1.4.170.
Original source files, copyright notices and README are retained.

DBX changes `G2DB.fromString` decimal binding to allocate enough `big.Float`
precision for the input decimal digits. The upstream default of 64 binary bits
loses fractional digits when a high precision decimal is imported as exact text.
REAL/DOUBLE conversion is unchanged. Run `go test ./...` in this nested module.

Optional log/statistics file creation failures return nil without printing to
stdout, preserving the main-process CLI's structured output. A regression test
covers both failure paths.
