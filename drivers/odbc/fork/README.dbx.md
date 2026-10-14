# Local ODBC driver fork

This source is copied from `github.com/alexbrainman/odbc` at version
`v0.0.0-20250601004241-49e6b2bc0cf0` (commit `49e6b2bc0cf0`). The original
`LICENSE`, `AUTHORS`, and `CONTRIBUTORS` are retained. It is selected only by
`drivers/odbc/go.mod`; the root module does not import or link ODBC.

DBX applies three focused changes:

- `rows.go` reports database column type names from the ODBC SQL type IDs.
  The upstream driver returns both text and binary as `[]byte`; this metadata
  lets dbcore keep BINARY/VARBINARY values as bytes even when valid UTF-8.
- `column.go` fetches NUMERIC/DECIMAL as full text instead of `SQL_C_DOUBLE`,
  which rounded values beyond float64 precision. The `columnForSQLType`
  extraction makes this selection testable without a live database.
- `stmt.go` keeps an unknown affected-row count unknown across multiple
  results and reports `SQLMoreResults` errors. DBX's guarded writes then
  reject unknown counts before commit.

Fork tests are `column_metadata_test.go` and `row_count_test.go`. Upstream's
live database integration tests are omitted from this focused source copy.
