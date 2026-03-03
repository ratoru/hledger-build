package runner

// Step represents a single build action: a command to run, its inputs (deps), and its output.
// All file paths are relative to the project root.
type Step struct {
	ID              string   // Unique identifier (typically the output path)
	Output          string   // Path to the output file (relative to project root)
	Deps            []string // Paths to dependency files (relative to project root)
	Command         string   // Binary to run ("hledger", "./preprocess", "./classify")
	Args            []string // Arguments to the command
	Cwd             string   // Working directory for the subprocess
	CaptureStdout   bool     // If true, capture stdout and write to Output
	Pass            int      // 1 or 2
	IsIngestJournal bool     // True for convert-stage outputs (used to build import stubs)
}
