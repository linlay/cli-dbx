package dm

import (
	"math/big"
	"testing"
)

func TestExactDecimalTextBinding(t *testing.T) {
	for _, text := range []string{
		"12345678901234567890.1234567890", "-12345678901234567890.1234567890",
		"0.1234567891", "123456789012345678901234567890",
		"99999999999999999999.9999999999", "-0.0000000001",
		"1.2345678901234567890123456789e19", "9.9999999999999999999999999999e-1",
	} {
		t.Run(text, func(t *testing.T) {
			param := parameter{column: column{colType: DECIMAL, prec: 40, scale: 10}}
			encoded, err := G2DB.fromString(text, param, nil)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := decodeDecimal(encoded.([]byte), 40, 10)
			if err != nil {
				t.Fatal(err)
			}
			want, _ := new(big.Rat).SetString(text)
			got, ok := new(big.Rat).SetString(decoded.String())
			if !ok || got.Cmp(want) != 0 {
				t.Fatalf("decimal bind = %s, want %s", decoded.String(), text)
			}
		})
	}
}
