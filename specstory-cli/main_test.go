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
	binary := filepath.Join(tempDir, "specstory")
	build := exec.Command(goBinary, "build", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building subprocess: %v\n%s", err, output)
	}

	for _, args := range [][]string{{"help"}, {"version"}, {"reindex"}} {
		name := strings.Join(args, "_")
		t.Run(name, func(t *testing.T) {
			tracePath := filepath.Join(tempDir, name+".trace")
			commandArgs := []string{"-f", "-qq", "-e", "trace=connect", "-e", "signal=none", "-o", tracePath, binary}
			commandArgs = append(commandArgs, args...)
			command := exec.Command(strace, commandArgs...)
			command.Env = append(os.Environ(),
				"HOME="+tempDir,
				"XDG_CONFIG_HOME="+filepath.Join(tempDir, "config"),
				"XDG_DATA_HOME="+filepath.Join(tempDir, "data"),
				"XDG_CACHE_HOME="+filepath.Join(tempDir, "cache"),
			)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("running %v: %v\n%s", args, err, output)
			}
			trace, err := os.ReadFile(tracePath)
			if err != nil {
				t.Fatalf("reading syscall trace: %v", err)
			}
			if len(strings.TrimSpace(string(trace))) != 0 {
				t.Fatalf("%v attempted a network connection:\n%s", args, trace)
			}
		})
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
