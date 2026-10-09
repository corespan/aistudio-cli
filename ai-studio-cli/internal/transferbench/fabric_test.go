package transferbench

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBisectionConfig(t *testing.T) {
	cfg, err := BisectionConfig(BisectionOptions{NumGPUs: 4, SubExecs: 8})
	if err != nil {
		t.Fatal(err)
	}
	lines := transferLines(cfg)
	want := []string{
		"1 8 (G0->G0->G2)",
		"2 8 (G0->G0->G2) (G1->G1->G3)",
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Errorf("transfer lines =\n%s\nwant\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
}

func TestBisectionConfigBidirectionalDMA(t *testing.T) {
	cfg, err := BisectionConfig(BisectionOptions{NumGPUs: 16, SubExecs: 1, DMA: true, Bidir: true})
	if err != nil {
		t.Fatal(err)
	}
	lines := transferLines(cfg)
	if len(lines) != 2 {
		t.Fatalf("want 2 tests, got %d:\n%s", len(lines), cfg)
	}
	if lines[0] != "2 1 (G0->D0->G8) (G8->D8->G0)" {
		t.Errorf("baseline = %q", lines[0])
	}
	// 8 pairs x 2 directions, all on one line so they run concurrently.
	if !strings.HasPrefix(lines[1], "16 1 (G0->D0->G8) (G8->D8->G0) (G1->D1->G9)") ||
		!strings.HasSuffix(lines[1], "(G7->D7->G15) (G15->D15->G7)") {
		t.Errorf("bisection line = %q", lines[1])
	}
}

func TestBisectionConfigRejects(t *testing.T) {
	for _, o := range []BisectionOptions{
		{NumGPUs: 0, SubExecs: 8},
		{NumGPUs: 1, SubExecs: 8},
		{NumGPUs: 7, SubExecs: 8},
		{NumGPUs: 8, SubExecs: 0},
	} {
		if _, err := BisectionConfig(o); err == nil {
			t.Errorf("expected an error for %+v", o)
		}
	}
}

// The comment and ## lines must not parse as Transfers: TransferBench skips a
// line whose first token is not a number.
func TestBisectionConfigCommentsAreNotTransfers(t *testing.T) {
	cfg, _ := BisectionConfig(BisectionOptions{NumGPUs: 2, SubExecs: 4})
	for _, l := range strings.Split(strings.TrimSpace(cfg), "\n") {
		if !strings.HasPrefix(l, "#") && !startsWithDigit(l) {
			t.Errorf("line is neither a comment nor a test: %q", l)
		}
	}
}

func TestDetectGPUs(t *testing.T) {
	root := t.TempDir()
	// Node 0 is a CPU socket, 1 and 2 are GPUs, 3 has no simd_count line.
	nodes := map[string]string{
		"0": "cpu_cores_count 64\nsimd_count 0\n",
		"1": "cpu_cores_count 0\nsimd_count 1216\n",
		"2": "cpu_cores_count 0\nsimd_count 1216\n",
		"3": "cpu_cores_count 0\nsimd_count_x 9\n",
	}
	for id, props := range nodes {
		dir := filepath.Join(root, "sys", "class", "kfd", "kfd", "topology", "nodes", id)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "properties"), []byte(props), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	n, err := DetectGPUs(root)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("DetectGPUs = %d, want 2", n)
	}
}

func TestDetectGPUsWithoutDriver(t *testing.T) {
	if _, err := DetectGPUs(t.TempDir()); err == nil {
		t.Error("expected an error when no KFD topology exists")
	}
}

func transferLines(cfg string) []string {
	var out []string
	for _, l := range strings.Split(cfg, "\n") {
		if startsWithDigit(l) {
			out = append(out, l)
		}
	}
	return out
}

func startsWithDigit(s string) bool {
	return s != "" && s[0] >= '0' && s[0] <= '9'
}
