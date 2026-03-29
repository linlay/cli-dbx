package cobra

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
)

type PositionalArgs func(cmd *Command, args []string) error

type CompletionOptions struct {
	DisableDefaultCmd bool
}

type boolFlag interface {
	IsBoolFlag() bool
}

type HelpField struct {
	Name        string
	Type        string
	Required    bool
	Default     string
	Description string
}

type Command struct {
	Use               string
	Short             string
	Long              string
	Example           string
	UsageLines        []string
	ArgFields         []HelpField
	Args              PositionalArgs
	RunE              func(cmd *Command, args []string) error
	SilenceUsage      bool
	SilenceErrors     bool
	Hidden            bool
	CompletionOptions CompletionOptions

	parent        *Command
	children      []*Command
	flagSet       *flag.FlagSet
	persistentSet *flag.FlagSet
	args          []string
	in            io.Reader
	out           io.Writer
	err           io.Writer
	ctx           context.Context
	required      map[string]struct{}
}

func (c *Command) AddCommand(children ...*Command) {
	for _, child := range children {
		if child == nil {
			continue
		}
		child.parent = c
		c.children = append(c.children, child)
	}
}

func (c *Command) SetArgs(args []string) {
	c.args = append([]string(nil), args...)
}

func (c *Command) SetIn(r io.Reader) {
	c.in = r
}

func (c *Command) SetOut(w io.Writer) {
	c.out = w
}

func (c *Command) SetErr(w io.Writer) {
	c.err = w
}

func (c *Command) SetContext(ctx context.Context) {
	c.ctx = ctx
}

func (c *Command) Context() context.Context {
	if c.ctx != nil {
		return c.ctx
	}
	if c.parent != nil {
		return c.parent.Context()
	}
	return context.Background()
}

func (c *Command) InOrStdin() io.Reader {
	if c.in != nil {
		return c.in
	}
	if c.parent != nil {
		return c.parent.InOrStdin()
	}
	return os.Stdin
}

func (c *Command) OutOrStdout() io.Writer {
	if c.out != nil {
		return c.out
	}
	if c.parent != nil {
		return c.parent.OutOrStdout()
	}
	return os.Stdout
}

func (c *Command) ErrOrStderr() io.Writer {
	if c.err != nil {
		return c.err
	}
	if c.parent != nil {
		return c.parent.ErrOrStderr()
	}
	return os.Stderr
}

func (c *Command) Flags() *flag.FlagSet {
	if c.flagSet == nil {
		c.flagSet = flag.NewFlagSet(c.Name(), flag.ContinueOnError)
		c.flagSet.SetOutput(io.Discard)
	}
	return c.flagSet
}

func (c *Command) PersistentFlags() *flag.FlagSet {
	if c.persistentSet == nil {
		c.persistentSet = flag.NewFlagSet(c.Name(), flag.ContinueOnError)
		c.persistentSet.SetOutput(io.Discard)
	}
	return c.persistentSet
}

func (c *Command) MarkFlagRequired(name string) error {
	if c.required == nil {
		c.required = make(map[string]struct{})
	}
	c.required[name] = struct{}{}
	return nil
}

func (c *Command) Name() string {
	name := strings.TrimSpace(c.Use)
	if name == "" {
		return ""
	}
	return strings.Fields(name)[0]
}

func (c *Command) CommandPath() string {
	if c.parent == nil {
		return c.Name()
	}
	parentPath := c.parent.CommandPath()
	if parentPath == "" {
		return c.Name()
	}
	if c.Name() == "" {
		return parentPath
	}
	return parentPath + " " + c.Name()
}

func (c *Command) Execute() error {
	return c.execute(c.args)
}

func (c *Command) Help() error {
	return c.printHelp()
}

func (c *Command) execute(args []string) error {
	persistentArgs, err := c.parseFlagSet(args, c.PersistentFlags(), false)
	if err != nil {
		return err
	}
	if err := c.validateRequired(c.PersistentFlags()); err != nil {
		return err
	}

	if len(persistentArgs) > 0 {
		switch persistentArgs[0] {
		case "help":
			return c.helpFor(persistentArgs[1:])
		case "-h", "--help":
			return c.printHelp()
		}
	}

	if child := c.findChild(persistentArgs); child != nil {
		return child.execute(persistentArgs[1:])
	}

	parsedArgs, err := c.parseFlagSet(persistentArgs, c.Flags(), true)
	if err != nil {
		return err
	}
	if err := c.validateRequired(c.Flags()); err != nil {
		return err
	}

	for _, arg := range parsedArgs {
		if arg == "-h" || arg == "--help" {
			return c.printHelp()
		}
	}

	if len(c.children) > 0 && len(parsedArgs) > 0 && !strings.HasPrefix(parsedArgs[0], "-") {
		return fmt.Errorf("unknown command %q for %q", parsedArgs[0], c.CommandPath())
	}
	if len(c.children) > 0 && len(parsedArgs) == 0 && c.RunE == nil {
		return c.printHelp()
	}

	if c.Args != nil {
		if err := c.Args(c, parsedArgs); err != nil {
			return err
		}
	}
	if c.RunE != nil {
		return c.RunE(c, parsedArgs)
	}
	return c.printHelp()
}

func (c *Command) helpFor(args []string) error {
	if len(args) == 0 {
		return c.printHelp()
	}

	target := c
	for _, arg := range args {
		next := target.findChild([]string{arg})
		if next == nil {
			return fmt.Errorf("unknown command %q for %q", arg, target.CommandPath()+" help")
		}
		target = next
	}
	return target.printHelp()
}

func (c *Command) findChild(args []string) *Command {
	if len(args) == 0 {
		return nil
	}
	name := args[0]
	for _, child := range c.children {
		if child.Name() == name {
			return child
		}
	}
	return nil
}

func (c *Command) parseFlagSet(args []string, fs *flag.FlagSet, allowUnknown bool) ([]string, error) {
	if fs == nil {
		return args, nil
	}

	positionals := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positionals = append(positionals, args[i+1:]...)
			break
		}
		if arg == "-h" || arg == "--help" {
			positionals = append(positionals, arg)
			continue
		}
		if !strings.HasPrefix(arg, "--") {
			positionals = append(positionals, arg)
			continue
		}

		nameValue := strings.TrimPrefix(arg, "--")
		name, value, hasValue := strings.Cut(nameValue, "=")
		f := fs.Lookup(name)
		if f == nil {
			if allowUnknown {
				return nil, fmt.Errorf("unknown flag: --%s", name)
			}
			positionals = append(positionals, arg)
			continue
		}

		if !hasValue {
			if bf, ok := f.Value.(boolFlag); ok && bf.IsBoolFlag() {
				value = "true"
			} else {
				i++
				if i >= len(args) {
					return nil, fmt.Errorf("flag needs an argument: --%s", name)
				}
				value = args[i]
			}
		}

		if err := f.Value.Set(value); err != nil {
			return nil, err
		}
	}
	return positionals, nil
}

func (c *Command) validateRequired(fs *flag.FlagSet) error {
	if fs == nil || len(c.required) == 0 {
		return nil
	}

	missing := make([]string, 0, len(c.required))
	for name := range c.required {
		f := fs.Lookup(name)
		if f == nil {
			continue
		}
		if f.Value.String() == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	sort.Strings(missing)
	if len(missing) == 1 {
		return fmt.Errorf("required flag(s) %q not set", missing[0])
	}
	return fmt.Errorf("required flag(s) %q not set", strings.Join(missing, `", "`))
}

func (c *Command) printHelp() error {
	_, err := fmt.Fprint(c.OutOrStdout(), c.helpText())
	return err
}

func (c *Command) helpText() string {
	var b strings.Builder

	usageLines := c.helpUsageLines()
	if len(usageLines) > 0 {
		b.WriteString("Usage:\n")
		for _, line := range usageLines {
			fmt.Fprintf(&b, "  %s\n", line)
		}
	}

	body := strings.TrimSpace(c.Long)
	if body == "" {
		body = strings.TrimSpace(c.Short)
	}
	if body != "" {
		b.WriteString("\nDescription:\n")
		b.WriteString(indentLines(body, "  "))
		b.WriteString("\n")
	}

	visibleChildren := make([]*Command, 0, len(c.children))
	for _, child := range c.children {
		if child.Hidden {
			continue
		}
		visibleChildren = append(visibleChildren, child)
	}
	if len(visibleChildren) > 0 {
		b.WriteString("\nSubcommands:\n")
		nameWidth := len("command")
		for _, child := range visibleChildren {
			if n := len(child.Name()); n > nameWidth {
				nameWidth = n
			}
		}
		for _, child := range visibleChildren {
			fmt.Fprintf(&b, "  %-*s   %s\n", nameWidth, child.Name(), child.Short)
		}
	}

	flagLines := c.flagHelpLines()
	b.WriteString("\nFlags:\n")
	for _, line := range flagLines {
		b.WriteString(line)
		b.WriteString("\n")
	}

	if len(c.ArgFields) > 0 {
		b.WriteString("\nArgs fields:\n")
		b.WriteString(c.argFieldTable())
	}

	if example := strings.TrimSpace(c.Example); example != "" {
		fmt.Fprintf(&b, "\nExamples:\n%s\n", indentLines(example, "  "))
	}

	return b.String()
}

func (c *Command) flagHelpLines() []string {
	lines := make([]string, 0)
	entries := make([]string, 0)
	seen := make(map[string]struct{})
	width := len("-h, --help")

	for _, fs := range c.collectFlagSets() {
		if fs == nil {
			continue
		}
		fs.VisitAll(func(f *flag.Flag) {
			if _, ok := seen[f.Name]; ok {
				return
			}
			seen[f.Name] = struct{}{}
			label := fmt.Sprintf("--%s", f.Name)
			if kind := flagValueType(f); kind != "" {
				label += " " + kind
			}
			entries = append(entries, fmt.Sprintf("%s\t%s", label, f.Usage))
			if len(label) > width {
				width = len(label)
			}
		})
	}

	for _, entry := range entries {
		label, usage, _ := strings.Cut(entry, "\t")
		lines = append(lines, fmt.Sprintf("  %-*s   %s", width, label, usage))
	}
	lines = append(lines, fmt.Sprintf("  %-*s   %s", width, "-h, --help", "help for this command"))
	return lines
}

func (c *Command) argFieldTable() string {
	headers := []string{"name", "type", "required", "default", "description"}
	widths := []int{
		len(headers[0]),
		len(headers[1]),
		len(headers[2]),
		len(headers[3]),
		len(headers[4]),
	}

	rows := make([][]string, 0, len(c.ArgFields))
	for _, field := range c.ArgFields {
		required := "no"
		if field.Required {
			required = "yes"
		}

		defaultValue := strings.TrimSpace(field.Default)
		if defaultValue == "" {
			defaultValue = "-"
		}

		row := []string{
			field.Name,
			helpFieldValue(field.Type, "string"),
			required,
			defaultValue,
			field.Description,
		}
		rows = append(rows, row)
		for i, value := range row {
			if len(value) > widths[i] {
				widths[i] = len(value)
			}
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "  %-*s   %-*s   %-*s   %-*s   %s\n",
		widths[0], headers[0],
		widths[1], headers[1],
		widths[2], headers[2],
		widths[3], headers[3],
		headers[4],
	)
	for _, row := range rows {
		fmt.Fprintf(&b, "  %-*s   %-*s   %-*s   %-*s   %s\n",
			widths[0], row[0],
			widths[1], row[1],
			widths[2], row[2],
			widths[3], row[3],
			row[4],
		)
	}
	return b.String()
}

func (c *Command) collectFlagSets() []*flag.FlagSet {
	sets := make([]*flag.FlagSet, 0, 4)
	for current := c; current != nil; current = current.parent {
		if current.persistentSet != nil {
			sets = append(sets, current.persistentSet)
		}
	}
	if c.flagSet != nil {
		sets = append(sets, c.flagSet)
	}
	return sets
}

func NoArgs(cmd *Command, args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("accepts 0 arg(s), received %d", len(args))
	}
	return nil
}

func ExactArgs(n int) PositionalArgs {
	return func(cmd *Command, args []string) error {
		if len(args) != n {
			return fmt.Errorf("accepts %d arg(s), received %d", n, len(args))
		}
		return nil
	}
}

func MinimumNArgs(n int) PositionalArgs {
	return func(cmd *Command, args []string) error {
		if len(args) < n {
			return fmt.Errorf("requires at least %d arg(s), received %d", n, len(args))
		}
		return nil
	}
}

func RangeArgs(min, max int) PositionalArgs {
	return func(cmd *Command, args []string) error {
		if len(args) < min || len(args) > max {
			return fmt.Errorf("accepts between %d and %d arg(s), received %d", min, max, len(args))
		}
		return nil
	}
}

func indentLines(raw, prefix string) string {
	lines := strings.Split(raw, "\n")
	for i := range lines {
		lines[i] = prefix + lines[i]
	}
	return strings.Join(lines, "\n")
}

func (c *Command) helpUsageLines() []string {
	if len(c.UsageLines) > 0 {
		lines := make([]string, 0, len(c.UsageLines))
		for _, line := range c.UsageLines {
			line = strings.TrimSpace(line)
			if line != "" {
				lines = append(lines, line)
			}
		}
		if len(lines) > 0 {
			return lines
		}
	}

	line := strings.TrimSpace(c.CommandPath())
	if line == "" {
		return nil
	}
	if c.Name() != "" && c.Use != "" && len(strings.Fields(c.Use)) > 1 {
		line += " " + strings.Join(strings.Fields(c.Use)[1:], " ")
	}
	return []string{line}
}

func helpFieldValue(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func flagValueType(f *flag.Flag) string {
	if f == nil {
		return ""
	}

	if _, err := strconv.ParseBool(f.DefValue); err == nil {
		return "boolean"
	}
	if _, err := strconv.Atoi(f.DefValue); err == nil {
		return "integer"
	}

	switch fmt.Sprintf("%T", f.Value) {
	case "*flag.boolValue", "flag.boolValue":
		return "boolean"
	case "*flag.intValue", "flag.intValue", "*flag.int64Value", "flag.int64Value":
		return "integer"
	default:
		return "string"
	}
}

func (c *Command) completionDisabled() bool {
	for current := c; current != nil; current = current.parent {
		if current.CompletionOptions.DisableDefaultCmd {
			return true
		}
	}
	return false
}
