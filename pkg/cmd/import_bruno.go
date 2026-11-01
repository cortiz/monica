package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"jmpeax.com/sec/monica/pkg/importer"
	"jmpeax.com/sec/monica/pkg/importer/bruno"
)

var (
	brunoOut   string
	brunoForce bool

	importBrunoCmd = &cobra.Command{
		Use:   "bruno <collection-dir>",
		Short: "Converts a Bruno collection (.bru files) into mon files and monica.yml",
		Long: `Converts a Bruno collection folder (with bruno.json) into a monica project:
folders and .bru requests become directories and .mon files in Bruno's seq order,
collection.bru headers and vars go to monica.yml defaults, environments/*.bru to
envs (vars:secret as ${NAME} placeholders), auth blocks become headers, and
scripts, tests and assert blocks keep working through a bru/req/res compatibility
module in lib/. collection.bru and folder.bru scripts become monica.js hooks.
Everything not converted is listed in IMPORT_REPORT.md.`,
		Example: `  monica import bruno ./bruno/shop -o shop`,
		Args:    cobra.ExactArgs(1),
		RunE:    runImportBruno,
	}
)

func init() {
	f := importBrunoCmd.Flags()
	f.StringVarP(&brunoOut, "out", "o", "", "folder to write the project to (default: the collection folder's name, in the current directory)")
	f.BoolVarP(&brunoForce, "force", "f", false, "write into a folder that is not empty (files with the same names are replaced)")
	importCmd.AddCommand(importBrunoCmd)
}

func runImportBruno(cmd *cobra.Command, args []string) error {
	cmd.SilenceUsage = true
	usage := func(err error) error { return &exitError{code: ExitUsage, err: err} }
	src := args[0]
	if fi, err := os.Stat(src); err != nil {
		return usage(err)
	} else if !fi.IsDir() {
		return usage(fmt.Errorf("%s is not a folder (pass the Bruno collection's folder)", src))
	}
	out := brunoOut
	if out == "" {
		abs, err := filepath.Abs(src)
		if err != nil {
			return usage(err)
		}
		out = importer.Slug(filepath.Base(abs))
	}
	if same, _ := sameDir(src, out); same {
		return usage(fmt.Errorf("--out must not be the collection folder itself"))
	}
	project, err := bruno.Convert(src)
	if err != nil {
		return usage(fmt.Errorf("%s: %w", src, err))
	}
	written, err := importer.WriteProject(out, project, brunoForce)
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

// sameDir reports whether a and b are the same folder.
func sameDir(a, b string) (bool, error) {
	ia, err := os.Stat(a)
	if err != nil {
		return false, err
	}
	ib, err := os.Stat(b)
	if err != nil {
		return false, err
	}
	return os.SameFile(ia, ib), nil
}
