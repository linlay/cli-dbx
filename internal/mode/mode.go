package mode

import (
	"fmt"
	"strings"
)

type Mode string

const (
	Lantern  Mode = "Lantern"
	Tweezers Mode = "Tweezers"
	Chisel   Mode = "Chisel"
	Forge    Mode = "Forge"
	Crown    Mode = "Crown"
	Wildfire Mode = "Wildfire"
)

type StatementClass string

const (
	ClassRead      StatementClass = "read"
	ClassWriteData StatementClass = "write-data"
	ClassDDL       StatementClass = "ddl"
	ClassAdmin     StatementClass = "admin"
	ClassUnknown   StatementClass = "unknown"
)

func Parse(raw string) (Mode, error) {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case "", "lantern":
		return Lantern, nil
	case "tweezers":
		return Tweezers, nil
	case "chisel":
		return Chisel, nil
	case "forge":
		return Forge, nil
	case "crown":
		return Crown, nil
	case "wildfire":
		return Wildfire, nil
	default:
		return "", fmt.Errorf("unknown mode %q", raw)
	}
}

func MustParse(raw string) Mode {
	m, err := Parse(raw)
	if err != nil {
		return Lantern
	}
	return m
}

func (m Mode) Allows(class StatementClass) bool {
	switch m {
	case Lantern:
		return class == ClassRead
	case Tweezers:
		return class == ClassRead || class == ClassWriteData
	case Chisel:
		return class == ClassRead || class == ClassDDL
	case Forge:
		return class == ClassRead || class == ClassWriteData || class == ClassDDL
	case Crown, Wildfire:
		return true
	default:
		return false
	}
}

func (m Mode) RequiresAck() bool {
	return m == Forge || m == Crown || m == Wildfire
}

func (m Mode) RiskLevel(class StatementClass) string {
	switch {
	case m == Wildfire:
		return "critical"
	case class == ClassAdmin:
		return "high"
	case class == ClassDDL:
		return "high"
	case class == ClassWriteData:
		return "medium"
	default:
		return "low"
	}
}
