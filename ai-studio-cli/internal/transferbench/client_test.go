package transferbench

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestArgs(t *testing.T) {
	cases := []struct {
		name   string
		bytes  string
		target string
		expr   []string
		want   []string
	}{
		{"no target prints topology", "", "", nil, nil},
		{"preset without size", "", "p2p", nil, []string{"p2p"}},
		{"preset with size", "1G", "a2a", nil, []string{"a2a", "1G"}},
		{"config file", "0", "/tmp/x.cfg", nil, []string{"/tmp/x.cfg", "0"}},
		// The expression starts at argv[3], so cmdline always carries a size.
		{"cmdline defaults size", "", "cmdline", []string{"1 4 (G0->G0->G1)"}, []string{"cmdline", "256M", "1 4 (G0->G0->G1)"}},
		{"cmdline with size", "64M", "cmdline", []string{"1", "4", "G0->G0->G1"}, []string{"cmdline", "64M", "1", "4", "G0->G0->G1"}},
		{"dryrun", "", "dryrun", []string{"1 4 (G0->G0->G1)"}, []string{"dryrun", "256M", "1 4 (G0->G0->G1)"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := NewRunner()
			r.Bytes = c.bytes
			got, err := r.args(c.target, c.expr)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got, c.want) {
				t.Errorf("args = %q, want %q", got, c.want)
			}
		})
	}
}

func TestArgsCmdlineNeedsExpression(t *testing.T) {
	if _, err := NewRunner().args("cmdline", nil); err == nil {
		t.Error("expected an error for cmdline without an expression")
	}
}

func TestEnviron(t *testing.T) {
	r := NewRunner()
	if env, _ := r.environ(nil); len(env) != 0 {
		t.Errorf("defaults should add no env vars, got %q", env)
	}

	r.Iterations = -5
	r.Warmups = "0"
	r.NumGPUs = 16
	r.CSV = true
	r.Env = []string{"A2A_MODE=1", "P2P_MODE=1"}
	got, err := r.environ(map[string]string{"USE_GPU_DMA": "1", "P2P_MODE": "0"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"NUM_ITERATIONS=-5", "NUM_WARMUPS=0", "NUM_GPU_DEVICES=16", "OUTPUT_TO_CSV=1",
		"P2P_MODE=0", "USE_GPU_DMA=1", // preset env, sorted
		"A2A_MODE=1", "P2P_MODE=1", // --env last, so it wins on duplicates
	}
	if !slices.Equal(got, want) {
		t.Errorf("environ =\n  %q\nwant\n  %q", got, want)
	}
}

func TestEnvironRejectsMalformed(t *testing.T) {
	for _, bad := range []string{"NOEQUALS", "=1"} {
		r := NewRunner()
		r.Env = []string{bad}
		if _, err := r.environ(nil); err == nil {
			t.Errorf("expected an error for --env %q", bad)
		}
	}
	for _, bad := range []string{"-1", "three"} {
		r := NewRunner()
		r.Warmups = bad
		if _, err := r.environ(nil); err == nil {
			t.Errorf("expected an error for --warmups %q", bad)
		}
	}
}

func TestCommandAppendsToProcessEnv(t *testing.T) {
	t.Setenv("TB_TEST_MARKER", "kept")
	r := NewRunner()
	r.CSV = true
	c, err := r.Command("p2p", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(c.Env, "TB_TEST_MARKER=kept") || !slices.Contains(c.Env, "OUTPUT_TO_CSV=1") {
		t.Error("command env should keep the parent environment and add TransferBench settings")
	}
	if !strings.HasPrefix(filepath.Base(c.Args[0]), BinaryName) {
		t.Errorf("unexpected binary %q", c.Args[0])
	}
}

func TestFindBinaryUsesROCmPath(t *testing.T) {
	rocm := t.TempDir()
	bin := filepath.Join(rocm, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	name := BinaryName
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if err := os.WriteFile(filepath.Join(bin, name), []byte{}, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ROCM_PATH", rocm)
	t.Setenv("PATH", t.TempDir()) // nothing on PATH

	if got := NewRunner().findBinary(); got != filepath.Join(bin, name) {
		t.Errorf("findBinary = %q, want %q", got, filepath.Join(bin, name))
	}
}
