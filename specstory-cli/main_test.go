package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func TestLocalCommandTreeExcludesOutboundSurfaces(t *testing.T) {
	root := createLocalCommandTree()
	if root.Use != "csessions [command]" {
		t.Errorf("root use = %q, want csessions [command]", root.Use)
	}
	if !strings.Contains(root.Long, "Codex Sessions") || strings.Contains(root.Long, "SpecStory") {
		t.Errorf("root description does not use Codex Sessions identity: %q", root.Long)
	}

	var commands []string
	for _, command := range root.Commands() {
		commands = append(commands, command.Name())
	}
	slices.Sort(commands)
	wantCommands := []string{"help", "reindex", "resume", "search", "version"}
	if !slices.Equal(commands, wantCommands) {
		t.Fatalf("local commands = %v, want %v", commands, wantCommands)
	}

	forbidden := []string{"analytics", "cloud", "login", "sync", "telemetry", "version-check"}
	var visit func(*cobra.Command)
	visit = func(command *cobra.Command) {
		for _, word := range forbidden {
			if strings.Contains(strings.ToLower(command.Name()), word) {
				t.Errorf("outbound command %q is reachable", command.CommandPath())
			}
		}
		command.LocalNonPersistentFlags().VisitAll(func(flag *pflag.Flag) {
			for _, word := range forbidden {
				if strings.Contains(strings.ToLower(flag.Name), word) {
					t.Errorf("outbound flag --%s is reachable on %q", flag.Name, command.CommandPath())
				}
			}
		})
		for _, child := range command.Commands() {
			visit(child)
		}
	}
	visit(root)
}

func TestLocalCommandsMakeNoNetworkConnections(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("network syscall audit is supported on Linux and WSL")
	}
	strace, err := exec.LookPath("strace")
	if err != nil {
		t.Skip("strace is required for the network syscall audit")
	}
	goBinary, err := exec.LookPath("go")
	if err != nil {
		t.Fatal("go binary is required to build the subprocess under test")
	}

	tempDir := t.TempDir()
	binary := filepath.Join(tempDir, "csessions")
	build := exec.Command(goBinary, "build", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building subprocess: %v\n%s", err, output)
	}

	commands := []struct {
		args         []string
		wantIdentity bool
	}{
		{args: nil, wantIdentity: true},
		{args: []string{"help"}, wantIdentity: true},
		{args: []string{"help", "resume"}, wantIdentity: true},
		{args: []string{"help", "search"}, wantIdentity: true},
		{args: []string{"help", "reindex"}, wantIdentity: true},
		{args: []string{"version"}, wantIdentity: true},
		{args: []string{"--version"}, wantIdentity: true},
		{args: []string{"reindex"}},
	}
	for _, tc := range commands {
		name := strings.Join(tc.args, "_")
		if name == "" {
			name = "startup"
		}
		t.Run(name, func(t *testing.T) {
			tracePath := filepath.Join(tempDir, name+".trace")
			commandArgs := []string{"-f", "-qq", "-e", "trace=connect", "-e", "signal=none", "-o", tracePath, binary}
			commandArgs = append(commandArgs, tc.args...)
			command := exec.Command(strace, commandArgs...)
			command.Env = append(os.Environ(),
				"HOME="+tempDir,
				"XDG_CONFIG_HOME="+filepath.Join(tempDir, "config"),
				"XDG_DATA_HOME="+filepath.Join(tempDir, "data"),
				"XDG_CACHE_HOME="+filepath.Join(tempDir, "cache"),
			)
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("running %v: %v\n%s", tc.args, err, output)
			}
			if tc.wantIdentity {
				text := string(output)
				hasCurrentIdentity := strings.Contains(text, "Codex Sessions") || strings.Contains(text, "csessions")
				if !hasCurrentIdentity || strings.Contains(text, "SpecStory") {
					t.Errorf("%v output has stale product identity:\n%s", tc.args, text)
				}
			}
			trace, err := os.ReadFile(tracePath)
			if err != nil {
				t.Fatalf("reading syscall trace: %v", err)
			}
			traceText := string(trace)
			if strings.Contains(traceText, "connect(") || strings.Contains(traceText, "connect resumed>") {
				t.Fatalf("%v attempted a network connection:\n%s", tc.args, trace)
			}
		})
	}

	legacyDir := filepath.Join(tempDir, ".specstory")
	if _, err := os.Stat(legacyDir); !os.IsNotExist(err) {
		t.Fatalf("local commands created legacy storage %q", legacyDir)
	}
	database := filepath.Join(tempDir, "data", "csessions", "sessions.db")
	if _, err := os.Stat(database); err != nil {
		t.Fatalf("reindex did not create XDG database %q: %v", database, err)
	}
}

func TestValidateFlags_CloudSyncMutualExclusion(t *testing.T) {
	// Save original global flag values
	origOnlyCloudSync := onlyCloudSync
	origNoCloudSync := noCloudSync
	origConsole := console
	origSilent := silent
	origDebug := debug
	origLogFile := logFile

	// Restore original values after test
	defer func() {
		onlyCloudSync = origOnlyCloudSync
		noCloudSync = origNoCloudSync
		console = origConsole
		silent = origSilent
		debug = origDebug
		logFile = origLogFile
	}()

	tests := []struct {
		name          string
		onlyCloudSync bool
		noCloudSync   bool
		expectError   bool
	}{
		{
			name:          "both flags set - mutually exclusive error",
			onlyCloudSync: true,
			noCloudSync:   true,
			expectError:   true,
		},
		{
			name:          "only-cloud-sync alone - valid",
			onlyCloudSync: true,
			noCloudSync:   false,
			expectError:   false,
		},
		{
			name:          "no-cloud-sync alone - valid",
			onlyCloudSync: false,
			noCloudSync:   true,
			expectError:   false,
		},
		{
			name:          "neither flag set - valid",
			onlyCloudSync: false,
			noCloudSync:   false,
			expectError:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set flags to known valid state for other validations
			console = false
			silent = false
			debug = false
			logFile = false

			// Set the flags under test
			onlyCloudSync = tt.onlyCloudSync
			noCloudSync = tt.noCloudSync

			err := validateFlags()

			if tt.expectError && err == nil {
				t.Errorf("validateFlags() expected error for onlyCloudSync=%v, noCloudSync=%v, got nil",
					tt.onlyCloudSync, tt.noCloudSync)
			}
			if !tt.expectError && err != nil {
				t.Errorf("validateFlags() unexpected error for onlyCloudSync=%v, noCloudSync=%v: %v",
					tt.onlyCloudSync, tt.noCloudSync, err)
			}
		})
	}
}
