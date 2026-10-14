package odbc

import (
	"testing"

	"github.com/alexbrainman/odbc/api"
)

func TestRowsColumnTypeDatabaseTypeNameSeparatesBinaryTextAndDecimal(t *testing.T) {
	rows := &Rows{os: &ODBCStmt{Cols: []Column{
		&NonBindableColumn{BaseColumn: &BaseColumn{SQLType: api.SQL_VARBINARY}},
		&NonBindableColumn{BaseColumn: &BaseColumn{SQLType: api.SQL_WVARCHAR}},
		&NonBindableColumn{BaseColumn: &BaseColumn{SQLType: api.SQL_DECIMAL}},
		&NonBindableColumn{BaseColumn: &BaseColumn{SQLType: api.SQL_LONGVARBINARY}},
	}}}
	for i, want := range []string{"VARBINARY", "WVARCHAR", "DECIMAL", "LONGVARBINARY"} {
		if got := rows.ColumnTypeDatabaseTypeName(i); got != want {
			t.Errorf("column %d type = %q, want %q", i, got, want)
		}
	}
}

func TestDecimalColumnUsesLosslessText(t *testing.T) {
	for _, sqlType := range []api.SQLSMALLINT{api.SQL_NUMERIC, api.SQL_DECIMAL} {
		column, err := columnForSQLType(&BaseColumn{SQLType: sqlType}, 38)
		if err != nil {
			t.Fatal(err)
		}
		c, ok := column.(*NonBindableColumn)
		if !ok || c.CType != api.SQL_C_CHAR {
			t.Fatalf("decimal column %#v must be unbound SQL_C_CHAR", column)
		}
		const exact = "90071992547409931234567890.123456789"
		value, err := c.BaseColumn.Value([]byte(exact))
		if err != nil || string(value.([]byte)) != exact {
			t.Fatalf("decimal value = %v, %v", value, err)
		}
	}
}
