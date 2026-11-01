package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"jmpeax.com/sec/monica/pkg/importer"
	"jmpeax.com/sec/monica/pkg/importer/httpfile"
)

var (
	httpOut   string
	httpForce bool

	importHTTPCmd = &cobra.Command{
		Use:   "http <file.http | file.rest | folder>",
		Short: "Converts JetBrains HTTP Client / VS Code REST Client files into mon files and monica.yml",
		Long: `Converts .http / .rest files (JetBrains HTTP Client, VS Code REST Client) into a
monica project. A folder is searched recursively and its structure kept. Requests
keep their ### titles, @name and @timeout; file variables (@name = value) go to
monica.yml defaults; http-client.env.json environments and VS Code's
rest-client.environmentVariables become envs (http-client.private.env.json values
as ${NAME} placeholders, listed in .env.example). Dynamic variables map to monica
built-ins or a computed value, VS Code request variables
({{login.response.body.$.token}}) to vars.set() in the named request, and JetBrains
response handlers and pre-request scripts keep working through a client / request /
response compatibility module in lib/. Everything not converted is listed in
IMPORT_REPORT.md.`,
		Example: `  monica import http requests.http -o api
  monica import http ./http -o api`,
		Args: cobra.ExactArgs(1),
		RunE: runImportHTTP,
	}
)

func init() {
	f := importHTTPCmd.Flags()
	f.StringVarP(&httpOut, "out", "o", "", "folder to write the project to (default: the file's or folder's name, in the current directory)")
	f.BoolVarP(&httpForce, "force", "f", false, "write into a folder that is not empty (files with the same names are replaced)")
	importCmd.AddCommand(importHTTPCmd)
}

func runImportHTTP(cmd *cobra.Command, args []string) error {
	cmd.SilenceUsage = true
	usage := func(err error) error { return &exitError{code: ExitUsage, err: err} }
	src := args[0]
	fi, err := os.Stat(src)
	if err != nil {
		return usage(err)
	}
	out := httpOut
	if out == "" {
		abs, err := filepath.Abs(src)
		if err != nil {
			return usage(err)
		}
		base := filepath.Base(abs)
		if !fi.IsDir() {
			base = strings.TrimSuffix(base, filepath.Ext(base))
		}
		out = importer.Slug(base)
	}
	srcDir := src
	if !fi.IsDir() {
		srcDir = filepath.Dir(src)
	}
	if same, _ := sameDir(srcDir, out); same {
		return usage(fmt.Errorf("--out must not be the folder of the .http files"))
	}
	project, err := httpfile.Convert(src)
	if err != nil {
		return usage(fmt.Errorf("%s: %w", src, err))
	}
	written, err := importer.WriteProject(out, project, httpForce)
	if err != nil {
		return usage(err)
	}
	n := 0
	for _, reqs := range project.Files {
		n += len(reqs)
	}
	w := cmd.ErrOrStderr()
	_, _ = fmt.Fprintf(w, "wrote %d requests (%d files) to %s\n", n, len(written), out)
	if len(project.Report) > 0 {
		_, _ = fmt.Fprintf(w, "%d items need a look: see %s\n", len(project.Report), filepath.Join(out, importer.ReportFile))
	}
	return nil
}
