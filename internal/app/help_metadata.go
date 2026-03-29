package app

import "github.com/spf13/cobra"

func helpField(name, typ string, required bool, description string) cobra.HelpField {
	return cobra.HelpField{
		Name:        name,
		Type:        typ,
		Required:    required,
		Default:     "-",
		Description: description,
	}
}
