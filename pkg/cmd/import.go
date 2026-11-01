package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strconv"

	"github.com/spf13/cobra"
	"jmpeax.com/sec/monica/pkg/importer"
	"jmpeax.com/sec/monica/pkg/importer/curl"
)

var (
	importOut   string
	importForce bool
	importName  string

	importCmd = &cobra.Command{
		Use:   "import",
		Short: "Converts requests from other tools into mon files",
	}

	importCurlCmd = &cobra.Command{
		Use:   "curl ['<curl command>' | - | -- curl args...]",
		Short: "Converts a curl command into a mon file",
		Long: `Converts a curl command (as copied with "Copy as cURL") into a mon request.
Pass the command as one quoted argument, "-" (or nothing) to read it from stdin,
or as separate arguments after "--" (already split by your shell). The mon text goes to stdout, or to --out.
Options that cannot be converted are listed as warnings on stderr.`,
		Example: `  monica import curl 'curl -X POST https://api.example.com/users -H "Content-Type: application/json" -d "{\"name\":\"ada\"}"'
  pbpaste | monica import curl -o users.mon
  monica import curl -- curl -sS https://api.example.com/health`,
		RunE: runImportCurl,
	}
)

func init() {
	f := importCurlCmd.Flags()
	f.StringVarP(&importOut, "out", "o", "", "write the mon file here instead of stdout")
	f.BoolVarP(&importForce, "force", "f", false, "overwrite an existing --out file")
	f.StringVar(&importName, "name", "", "@name of the request (requests after the first get name2, name3, ...)")
	importCmd.AddCommand(importCurlCmd)
	rootCmd.AddCommand(importCmd)
}

func runImportCurl(cmd *cobra.Command, args []string) error {
	usage := func(err error) error { return &exitError{code: ExitUsage, err: err} }
	cmd.SilenceUsage = true
	var (
		res *curl.Result
		err error
	)
	switch {
	case len(args) == 0 || (len(args) == 1 && args[0] == "-"):
		in, rerr := io.ReadAll(cmd.InOrStdin())
		if rerr != nil {
			return usage(fmt.Errorf("reading stdin: %w", rerr))
		}
		res, err = curl.ParseCommand(string(in))
	case len(args) == 1:
		res, err = curl.ParseCommand(args[0])
	default:
		res, err = curl.Parse(args)
	}
	if err != nil {
		return usage(fmt.Errorf("curl: %w", err))
	}
	if importName != "" {
		for i := range res.Requests {
			res.Requests[i].Name = importName
			if i > 0 {
				res.Requests[i].Name += strconv.Itoa(i + 1)
			}
		}
	}
	var buf bytes.Buffer
	if err := importer.WriteMon(&buf, res.Requests); err != nil {
		return usage(err)
	}
	for _, w := range res.Warnings {
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "warning:", w)
	}
	if importOut == "" {
		_, err := cmd.OutOrStdout().Write(buf.Bytes())
		return err
	}
	flags := os.O_WRONLY | os.O_CREATE | os.O_EXCL
	if importForce {
		flags = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	}
	f, err := os.OpenFile(importOut, flags, 0o644) //nolint:gosec // a .mon file the user asked for
	if errors.Is(err, fs.ErrExist) {
		return usage(fmt.Errorf("%s exists (use --force to overwrite)", importOut))
	}
	if err != nil {
		return usage(err)
	}
	if _, err := f.Write(buf.Bytes()); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "wrote %s\n", importOut)
	return nil
}
