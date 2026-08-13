// Command mkinstaller packs dist/agent into the reference installer archive
// the server hands out.
//
// The archive is built once per release and never per download: the server
// copies its entries through verbatim and substitutes agent.env, which is why
// Guardian.exe can be signed once and why no Go toolchain is needed in the
// container. See specs/2026-09-05-saas-design.md, "Installer".
//
// Usage:
//
//	go run ./tools/mkinstaller -in dist/agent -out release/Guardian.zip
//
// It is deliberately picky: an archive missing an agent binary, or one
// carrying a real binding token, is a release nobody wants to find out about
// from a customer.
package main

import (
	"archive/zip"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// envName is the file the server replaces on every download. It is shipped in
// the reference archive as a template so the folder also works when installed
// by hand, but it must never carry a live token.
const envName = "agent.env"

// placeholderToken is what the shipped template says instead of a token. The
// check below is on the token line's shape, not on this exact string, so a
// reworded template does not silently disable it.
const placeholderToken = "your_binding_token_here"

func main() {
	in := flag.String("in", filepath.Join("dist", "agent"), "directory to pack")
	out := flag.String("out", filepath.Join("release", "Guardian.zip"), "archive to write")
	flag.Parse()

	if err := run(*in, *out); err != nil {
		fmt.Fprintln(os.Stderr, "mkinstaller:", err)
		os.Exit(1)
	}
}

func run(in, out string) error {
	files, err := collect(in)
	if err != nil {
		return err
	}
	if err := check(files); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	f, err := os.Create(out)
	if err != nil {
		return err
	}
	defer f.Close()

	zw := zip.NewWriter(f)
	for _, name := range sortedNames(files) {
		w, err := zw.Create(name)
		if err != nil {
			return err
		}
		src, err := os.Open(files[name])
		if err != nil {
			return err
		}
		_, err = io.Copy(w, src)
		src.Close()
		if err != nil {
			return err
		}
	}
	if err := zw.Close(); err != nil {
		return err
	}

	info, err := f.Stat()
	if err != nil {
		return err
	}
	fmt.Printf("%s: %d files, %d bytes\n", out, len(files), info.Size())
	return nil
}

// collect maps archive entry name → source path. Entry names always use
// forward slashes, whatever the host separator is.
func collect(dir string) (map[string]string, error) {
	files := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(rel)] = path
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", dir, err)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("%s holds no files", dir)
	}
	return files, nil
}

// check refuses the two archives that would be discovered in production: one
// that installs nothing, and one that ships a customer's credential.
func check(files map[string]string) error {
	if !hasAgent(files) {
		return fmt.Errorf("no procsentinel-agent32.exe or procsentinel-agent64.exe found; " +
			"build the agent first (agent/build32.ps1, agent/build64.ps1)")
	}
	path, ok := files[envName]
	if !ok {
		return fmt.Errorf("%s is missing; the archive must ship the template the console reads", envName)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if tok := bindingTokenIn(string(raw)); tok != "" && tok != placeholderToken {
		return fmt.Errorf("%s carries what looks like a real binding token (%q); "+
			"the reference archive must ship the placeholder", envName, tok)
	}
	return nil
}

func hasAgent(files map[string]string) bool {
	for name := range files {
		lower := strings.ToLower(name)
		if strings.HasSuffix(lower, "procsentinel-agent32.exe") ||
			strings.HasSuffix(lower, "procsentinel-agent64.exe") {
			return true
		}
	}
	return false
}

// bindingTokenIn returns the value of the BINDING_TOKEN line, if there is one.
// Commented-out lines do not count: a token behind a # is not a token the
// agent would read.
func bindingTokenIn(env string) string {
	for _, line := range strings.Split(env, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if strings.HasPrefix(line, "#") {
			continue
		}
		if v, ok := strings.CutPrefix(line, "BINDING_TOKEN="); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func sortedNames(files map[string]string) []string {
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
