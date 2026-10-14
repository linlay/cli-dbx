package dm

import (
	"math/big"
	"testing"
)

// A short exponent spelling still describes a 38-digit exact integer. Binding
// precision cannot depend solely on the source string's character count.
func TestExactDecimalScientificIntegerBinding(t *testing.T) {
	for _, text := range []string{"1e37", "-1e37", "1.2345e37"} {
		t.Run(text, func(t *testing.T) {
			param := parameter{column: column{colType: DECIMAL, prec: 38, scale: 0}}
			encoded, err := G2DB.fromString(text, param, nil)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := decodeDecimal(encoded.([]byte), 38, 0)
			if err != nil {
				t.Fatal(err)
			}
			want, _ := new(big.Rat).SetString(text)
			got, ok := new(big.Rat).SetString(decoded.String())
			if !ok || got.Cmp(want) != 0 {
				t.Fatalf("scientific decimal bind = %s, want %s", decoded.String(), text)
			}
		})
	}
}
