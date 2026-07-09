package app

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/linlay/cli-dbx/internal/secret"
	"github.com/spf13/cobra"
)

func withSecretProvider(ctx context.Context, stdin io.Reader, stderr io.Writer) context.Context {
	reader := bufio.NewReader(stdin)
	return secret.WithPassphraseProvider(ctx, func(context.Context) (string, error) {
		return readSecretLine(reader, stderr, "master passphrase: ")
	})
}

func newSecretCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "secret",
		Short: "Encrypt DBX secrets for config files",
		Long: strings.TrimSpace(`
Use secret to produce encrypted values that can be stored directly in a DBX
connection TOML file.
`),
		UsageLines: []string{
			"dbx secret [command]",
		},
		Example: strings.TrimSpace(`
dbx secret encrypt
`),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	cmd.AddCommand(newSecretEncryptCommand())
	return cmd
}

func newSecretEncryptCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "encrypt",
		Short: "Encrypt a database password for password = \"...\"",
		Long: strings.TrimSpace(`
Encrypt a database password using AES-256-GCM. The command prompts for a master
passphrase and the database password, then prints a TOML password line.
`),
		UsageLines: []string{
			"dbx secret encrypt",
		},
		Example: strings.TrimSpace(`
dbx secret encrypt
`),
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			reader := bufio.NewReader(cmd.InOrStdin())
			passphrase, err := readSecretLine(reader, cmd.ErrOrStderr(), "master passphrase: ")
			if err != nil {
				return failuref("%v", err)
			}
			password, err := readSecretLine(reader, cmd.ErrOrStderr(), "database password: ")
			if err != nil {
				return failuref("%v", err)
			}
			encrypted, err := secret.EncryptString(passphrase, password)
			if err != nil {
				return failuref("%v", err)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "password = %q\n", encrypted)
			return err
		},
	}
	return cmd
}

func readSecretLine(reader *bufio.Reader, promptOut io.Writer, prompt string) (string, error) {
	if promptOut != nil {
		_, _ = fmt.Fprint(promptOut, prompt)
	}
	line, err := reader.ReadString('\n')
	if promptOut != nil {
		_, _ = fmt.Fprintln(promptOut)
	}
	if err != nil && line == "" {
		return "", fmt.Errorf("read %s: %w", strings.TrimSpace(strings.TrimSuffix(prompt, ": ")), err)
	}
	return strings.TrimRight(line, "\r\n"), nil
}
