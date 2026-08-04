package main

import (
	"os"
	"strings"
)

// Running the console straight out of the downloaded ZIP is the most common
// way this product is installed wrong, and it is not the user's fault:
// Explorer shows an archive as a folder, so double-clicking Guardian.exe
// inside it looks exactly like double-clicking it after extracting. Windows
// then copies that ONE file to a temporary directory and runs it there,
// leaving agent.env and both agent binaries behind in the archive. Every
// failure that follows is a technical one about a missing file.
//
// So the console checks its own surroundings at startup and says the one
// useful sentence instead. See specs/2026-09-05-saas-design.md, "Installer".

// archiveVerdict is what the check below concluded about the folder the
// console is running from.
type archiveVerdict int

const (
	// archiveOK: the payload is next to us, wherever we are.
	archiveOK archiveVerdict = iota
	// archiveUnextracted: we are running from a temporary copy made by an
	// archiver, with the rest of the archive nowhere near. Fatal — nothing
	// this program does would work.
	archiveUnextracted
	// archivePayloadMissing: a normal folder, but with no agent binaries in
	// it. The console can still diagnose and control an installed agent, so
	// this is a warning rather than an exit.
	archivePayloadMissing
)

// inspectSurroundings decides which of the three cases applies.
//
// dir is the folder holding this executable and temp is the value of %TEMP%.
// Both are parameters rather than lookups so the whole rule is testable off
// Windows, where the rest of this program cannot even build.
func inspectSurroundings(dir, temp string, payloadPresent bool) archiveVerdict {
	if payloadPresent {
		return archiveOK
	}
	if looksLikeArchivePreview(dir, temp) {
		return archiveUnextracted
	}
	return archivePayloadMissing
}

// looksLikeArchivePreview recognises the scratch directories archivers run a
// single extracted file from. Explorer's own is %TEMP%\Temp1_<archive>\ (the
// number increments per open); WinRAR and 7-Zip have their own, and some
// tools present the archive itself as a path segment.
//
// The %TEMP% test is deliberately not the only one: a user with TEMP pointed
// somewhere unusual still gets the right message, and a folder that is
// literally named after a .zip is never a real installation.
func looksLikeArchivePreview(dir, temp string) bool {
	if dir == "" {
		return false
	}
	norm := strings.ReplaceAll(dir, "/", `\`)
	segments := strings.Split(norm, `\`)

	underTemp := temp != "" && isUnder(norm, strings.ReplaceAll(temp, "/", `\`))

	for _, seg := range segments {
		lower := strings.ToLower(seg)
		switch {
		// Explorer: Temp1_Guardian.zip, Temp2_Guardian.zip, ...
		case strings.HasPrefix(lower, "temp") && strings.Contains(lower, "_"):
			if underTemp || strings.Contains(lower, ".zip") {
				return true
			}
		// WinRAR: Rar$EXa0.123, 7-Zip: 7zO8A3C2E1B
		case strings.HasPrefix(lower, "rar$"), strings.HasPrefix(lower, "7z"):
			if underTemp {
				return true
			}
		// Some shells expose the archive itself as a folder in the path.
		case strings.HasSuffix(lower, ".zip"), strings.HasSuffix(lower, ".rar"),
			strings.HasSuffix(lower, ".7z"):
			return true
		}
	}
	return false
}

// isUnder reports whether path sits inside dir, comparing case-insensitively
// the way Windows paths do.
func isUnder(path, dir string) bool {
	path = strings.ToLower(strings.TrimRight(path, `\`))
	dir = strings.ToLower(strings.TrimRight(dir, `\`))
	if dir == "" {
		return false
	}
	return path == dir || strings.HasPrefix(path, dir+`\`)
}

// payloadPresent reports whether the files the console installs are next to
// it. Either architecture counts: a customer who deleted the one they do not
// need still has a working folder.
func payloadPresent(dir string) bool {
	for _, arch := range []string{"64", "32"} {
		if findAgentExe(dir, arch) != "" {
			return true
		}
	}
	return false
}

// unextractedMessage is the sentence this whole file exists to produce.
const unextractedMessage = "This program was started from inside the downloaded archive, " +
	"so the files it needs are not next to it.\n\n" +
	"Close this window, right-click Guardian.zip, choose \"Extract All...\", " +
	"and run Guardian.exe from the extracted folder."

// payloadMissingMessage is the softer case: a real folder, but not the one
// the archive produced.
const payloadMissingMessage = "The agent files are not in this folder, so installing and " +
	"updating the agent will not work.\n\n" +
	"Run Guardian.exe from the folder the downloaded archive was extracted to. " +
	"Managing an agent that is already installed still works from here."

// surroundings is the startup check itself, reading the real environment.
func surroundings() (archiveVerdict, string) {
	dir := guiDir()
	verdict := inspectSurroundings(dir, os.Getenv("TEMP"), payloadPresent(dir))
	switch verdict {
	case archiveUnextracted:
		return verdict, unextractedMessage
	case archivePayloadMissing:
		return verdict, payloadMissingMessage
	}
	return archiveOK, ""
}
