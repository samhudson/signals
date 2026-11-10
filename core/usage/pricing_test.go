package usage

import (
	"bytes"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func mustPricing(t *testing.T) *Pricing {
	t.Helper()
	p, err := LoadPricing("")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// The meter (libexec) and the engine read the same table.
func TestPricingFilesMatch(t *testing.T) {
	lib, err := os.ReadFile(filepath.Join("..", "..", "libexec", "pricing.json"))
	if err != nil {
		t.Skip("libexec/pricing.json not found (not in the repo)")
	}
	if !bytes.Equal(lib, embeddedPricing) {
		t.Fatal("core/usage/pricing.json and libexec/pricing.json differ: copy libexec/pricing.json over")
	}
}

func TestCostClaude(t *testing.T) {
	p := mustPricing(t)
	m := int64(1_000_000)
	c := Call{M: "claude-opus-5-5", D: "2026-11-09", In: m, Out: m, CR: m, CW5: m, CW1h: m}
	// $4 in + $20 out + $0.20 cache read + $5 (1.25x) 5-minute writes + $8 (2x) 1-hour writes
	if got, ok := p.Cost(c); !ok || !near(got, 37.2) {
		t.Errorf("opus 5.5: %v %v, want 37.2", got, ok)
	}
	c.Fast = true
	if got, _ := p.Cost(c); !near(got, 74.4) {
		t.Errorf("fast mode doubles: %v", got)
	}
	// Sonnet 5.5 cache reads are $0.10 (Nov 2026 price page).
	if got, _ := p.Cost(Call{M: "claude-sonnet-5-5", CR: m}); !near(got, 0.10) {
		t.Errorf("sonnet 5.5 cache read: %v", got)
	}
	for _, name := range []string{"claude-sonnet-4-5-20250929", "us.anthropic.claude-sonnet-4-5-20250929-v1:0", "claude-sonnet-4-5@20250929", "claude-sonnet-4-5[1m]"} {
		if got, ok := p.Cost(Call{M: name, In: m}); !ok || !near(got, 3) {
			t.Errorf("%s: %v %v, want 3", name, got, ok)
		}
	}
	if got, ok := p.Cost(Call{M: "claude-opus-4-9", In: m}); !ok || !near(got, 5) {
		t.Errorf("unknown Opus falls back to the Opus family: %v %v", got, ok)
	}
}

func TestCostLongPrompt(t *testing.T) {
	p := mustPricing(t)
	short := Call{M: "claude-haiku-5-5", In: 50_000, CR: 40_000, Out: 1_000}
	// 50k x $0.10 + 40k x $0.01 + 1k x $0.50 per million
	if got, _ := p.Cost(short); !near(got, 0.0059) {
		t.Errorf("haiku 5.5 short: %v", got)
	}
	long := Call{M: "claude-haiku-5-5", In: 50_000, CR: 60_000, Out: 1_000}
	// over 100k prompt tokens (cache reads count): 50k x $0.50 + 60k x $0.05 + 1k x $2.50
	if got, _ := p.Cost(long); !near(got, 0.0305) {
		t.Errorf("haiku 5.5 long: %v", got)
	}
}

func TestCostOpenAI(t *testing.T) {
	p := mustPricing(t)
	c := Call{M: "gpt-6.1-sol", In: 10_000, CR: 20_000, CW5: 1_000, Out: 2_000}
	// 10k x $2 + 20k x $0.10 + 1k x $2.50 (listed cache write) + 2k x $10
	if got, ok := p.Cost(c); !ok || !near(got, 0.0445) {
		t.Errorf("gpt-6.1-sol: %v %v", got, ok)
	}
	long := Call{M: "gpt-6.1-sol", In: 300_000, Out: 1_000, CW5: 1_000}
	// over 272k: $4 in, $15 out; the long cache write scales with input: $2.50 x 4/2 = $5
	if got, _ := p.Cost(long); !near(got, (300_000*4+1_000*15+1_000*5)/1e6) {
		t.Errorf("gpt-6.1-sol long: %v", got)
	}
	if got, ok := p.Cost(Call{M: "gpt-5.3-codex", CW5: 1_000_000}); !ok || !near(got, 1.75*1.25) {
		t.Errorf("unlisted cache write uses the vendor rule (1.25x input): %v", got)
	}
}

func TestCostDatedChange(t *testing.T) {
	p := mustPricing(t)
	before := Call{M: "gemini-4-argon", D: "2026-11-30", In: 1_000_000}
	after := before
	after.D = "2026-12-01"
	if got, _ := p.Cost(before); !near(got, 2) {
		t.Errorf("before Dec 1: %v", got)
	}
	if got, _ := p.Cost(after); !near(got, 4) {
		t.Errorf("from Dec 1: %v", got)
	}
}

func TestUnknownModelsAreNeverGuessed(t *testing.T) {
	p := mustPricing(t)
	for _, m := range []string{"gpt-9-turbo", "gemini-9-ultra", "", "<synthetic>"} {
		if _, ok := p.Cost(Call{M: m, In: 1000}); ok {
			t.Errorf("%q should be unpriced", m)
		}
	}
}

func TestPricingOverride(t *testing.T) {
	f := filepath.Join(t.TempDir(), "pricing.json")
	os.WriteFile(f, []byte(`{"models":{"gpt-9-turbo":{"vendor":"openai","input":1,"output":2,"cache_read":0.1}}}`), 0o644)
	p, err := LoadPricing(f)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := p.Cost(Call{M: "gpt-9-turbo", In: 1_000_000}); !ok || !near(got, 1) {
		t.Errorf("override: %v %v", got, ok)
	}
	if p.Hash() == mustPricing(t).Hash() {
		t.Error("a different table has a different hash")
	}
}
