// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"archive/zip"
	"fmt"
)

func init() { extraCommands["import-zip"] = runImportZip }

// runImportZip creates an own project from a ZIP archive (T-9.2).
func runImportZip(args []string, sys system) int {
	fset, data := dataFlagSet("import-zip", sys)
	name := fset.String("name", "", "")
	owner := fset.String("owner", "", "")
	tags := fset.String("tags", "", "")
	pos, code, ok := parseArgs(fset, args, 1, 1, sys)
	if !ok {
		return code
	}
	zr, err := zip.OpenReader(pos[0])
	if err != nil {
		fmt.Fprintln(sys.stderr, errText(err))
		return ExitError
	}
	defer func() { _ = zr.Close() }()
	ctx, cancel := cmdContext()
	defer cancel()
	lib, code, ok := openLibrary(ctx, *data, sys)
	if !ok {
		return code
	}
	defer lib.Close()
	r, err := lib.ImportZIP(ctx, &zr.Reader, *owner, *name, "", splitList(*tags), "cli")
	if err != nil {
		fmt.Fprintln(sys.stderr, errText(err))
		return ExitError
	}
	fmt.Fprintf(sys.stdout, msgCreated, r.Entry.Key, "/git/"+r.Entry.Key+".git")
	return ExitOK
}
