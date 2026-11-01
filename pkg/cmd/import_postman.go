package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"jmpeax.com/sec/monica/pkg/importer"
	"jmpeax.com/sec/monica/pkg/importer/postman"
)

var (
	postmanEnvs  []string
	postmanOut   string
	postmanForce bool

	importPostmanCmd = &cobra.Command{
		Use:   "postman <collection.json>",
		Short: "Converts a Postman collection (v2.1) into mon files and monica.yml",
		Long: `Converts a Postman collection exported as Collection v2.1 (or v2.0) into a
monica project: folders become directories, each request a .mon file, collection
variables and --env files go to monica.yml (secret values as ${NAME}
placeholders), auth blocks become headers, and scripts keep working through a
pm compatibility module in lib/. Collection and folder scripts become monica.js
hooks. Everything not converted is listed in IMPORT_REPORT.md.`,
		Example: `  monica import postman shop.postman_collection.json --env dev.postman_environment.json -o shop`,
		Args:    cobra.ExactArgs(1),
		RunE:    runImportPostman,
	}
)

func init() {
	f := importPostmanCmd.Flags()
	f.StringArrayVarP(&postmanEnvs, "env", "e", nil, "a Postman environment file to add as an environment (repeatable)")
	f.StringVarP(&postmanOut, "out", "o", "", "folder to write the project to (default: the collection's name)")
	f.BoolVarP(&postmanForce, "force", "f", false, "write into a folder that is not empty (files with the same names are replaced)")
	importCmd.AddCommand(importPostmanCmd)
}

func runImportPostman(cmd *cobra.Command, args []string) error {
	cmd.SilenceUsage = true
	usage := func(err error) error { return &exitError{code: ExitUsage, err: err} }
	data, err := os.ReadFile(args[0])
	if err != nil {
		return usage(err)
	}
	coll, err := postman.ParseCollection(data)
	if err != nil {
		return usage(fmt.Errorf("%s: %w", args[0], err))
	}
	var envs []*postman.Environment
	for _, path := range postmanEnvs {
		data, err := os.ReadFile(path)
		if err != nil {
			return usage(err)
		}
		env, err := postman.ParseEnvironment(data)
		if err != nil {
			return usage(fmt.Errorf("%s: %w", path, err))
		}
		envs = append(envs, env)
	}
	out := postmanOut
	if out == "" {
		out = importer.Slug(coll.Info.Name)
	}
	project := postman.Convert(coll, envs)
	written, err := importer.WriteProject(out, project, postmanForce)
	if err != nil {
		return usage(err)
	}
	w := cmd.ErrOrStderr()
	n := 0
	for _, reqs := range project.Files {
		n += len(reqs)
	}
	_, _ = fmt.Fprintf(w, "wrote %d requests (%d files) to %s\n", n, len(written), out)
	if len(project.Report) > 0 {
		_, _ = fmt.Fprintf(w, "%d items need a look: see %s\n", len(project.Report), filepath.Join(out, importer.ReportFile))
	}
	return nil
}
