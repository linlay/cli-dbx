package app

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/linlay/cli-dbx/internal/secret"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func withSecretProvider(ctx context.Context, stdin io.Reader, stderr io.Writer) context.Context {
	ctx = secret.WithSystemKeyStore(ctx)
	return secret.WithPassphraseProvider(ctx, func(context.Context) (string, error) {
		return readSecretLine(stdin, stderr, "master passphrase: ")
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
Encrypt a database password using AES-256-GCM. DBX stores the encryption key in
the operating system credential store and prints a machine-bound TOML password
line. It never prints the plaintext password or encryption key.
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
			password, err := readSecretLine(cmd.InOrStdin(), cmd.ErrOrStderr(), "database password: ")
			if err != nil {
				return failuref("%v", err)
			}
			encrypted, err := secret.EncryptStringV2(cmd.Context(), password)
			if err != nil {
				return failuref("%v", err)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "password = %q\n", encrypted)
			return err
		},
	}
	return cmd
}

func readSecretLine(reader io.Reader, promptOut io.Writer, prompt string) (string, error) {
	if promptOut != nil {
		_, _ = fmt.Fprint(promptOut, prompt)
	}
	if file, ok := reader.(*os.File); ok && term.IsTerminal(int(file.Fd())) {
		line, err := term.ReadPassword(int(file.Fd()))
		if promptOut != nil {
			_, _ = fmt.Fprintln(promptOut)
		}
		if err != nil {
			return "", fmt.Errorf("read %s: %w", strings.TrimSpace(strings.TrimSuffix(prompt, ": ")), err)
		}
		return strings.TrimRight(string(line), "\r\n"), nil
	}
	line, err := bufio.NewReader(reader).ReadString('\n')
	if promptOut != nil {
		_, _ = fmt.Fprintln(promptOut)
	}
	if err != nil && line == "" {
		return "", fmt.Errorf("read %s: %w", strings.TrimSpace(strings.TrimSuffix(prompt, ": ")), err)
	}
	return strings.TrimRight(line, "\r\n"), nil
}
