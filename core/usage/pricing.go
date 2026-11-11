package usage

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"os"
	"sort"
	"strings"
)

// The price table. libexec/pricing.json is the same file (the macOS meter
// reads it); a test keeps the two identical.
//
//go:embed pricing.json
var embeddedPricing []byte

type rates struct {
	Input      float64  `json:"input"`
	Output     float64  `json:"output"`
	CacheRead  float64  `json:"cache_read"`
	CacheWrite *float64 `json:"cache_write"`
}

type longRates struct {
	Above int64 `json:"above"`
	rates
}

type dated struct {
	From string `json:"from"`
	rates
}

type modelPrice struct {
	Vendor string `json:"vendor"`
	rates
	Long    *longRates `json:"long"`
	Changes []dated    `json:"changes"`
}

type vendorRules struct {
	Source       string  `json:"source"`
	Checked      string  `json:"checked"`
	CacheWrite5m float64 `json:"cache_write_5m"`
	CacheWrite1h float64 `json:"cache_write_1h"`
	CacheWrite   float64 `json:"cache_write"`
	Fast         float64 `json:"fast"`
}

// Pricing is the list-price table: per model, per vendor rules.
type Pricing struct {
	Updated  string                 `json:"updated"`
	Vendors  map[string]vendorRules `json:"vendors"`
	Models   map[string]modelPrice  `json:"models"`
	Families map[string]modelPrice  `json:"families"`
	hash     string
	keys     []string // model keys, longest first, for prefix matching
}

// LoadPricing reads the price table: the override file when there is one (as
// the meter does), else the built-in table.
func LoadPricing(override string) (*Pricing, error) {
	data := embeddedPricing
	if override != "" {
		if b, err := os.ReadFile(override); err == nil {
			data = b
		}
	}
	return parsePricing(data)
}

func parsePricing(data []byte) (*Pricing, error) {
	var p Pricing
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	p.hash = hex.EncodeToString(sum[:])[:12]
	for k := range p.Models {
		p.keys = append(p.keys, k)
	}
	sort.Slice(p.keys, func(i, j int) bool {
		if len(p.keys[i]) != len(p.keys[j]) {
			return len(p.keys[i]) > len(p.keys[j])
		}
		return p.keys[i] < p.keys[j]
	})
	return &p, nil
}

// Hash identifies this version of the prices (stored with rollups, so totals
// can be re-priced when prices change).
func (p *Pricing) Hash() string { return p.hash }

// normalize turns the model names cloud platforms use into the vendor's own:
// us.anthropic.claude-... (Bedrock), claude-...@date (Vertex), models/gemini-...,
// and the [1m] context suffix.
func normalize(model string) string {
	m := strings.ToLower(strings.TrimSpace(model))
	m = strings.TrimPrefix(m, "models/")
	if i := strings.Index(m, "anthropic."); i >= 0 && i <= 7 {
		m = m[i+len("anthropic."):]
	}
	if i := strings.IndexAny(m, "@["); i > 0 {
		m = m[:i]
	}
	return m
}

// lookup finds a model's prices the way the meter does: the exact name, then
// the longest name it starts with, then a Claude family name it contains.
func (p *Pricing) lookup(model string) (modelPrice, string, bool) {
	m := normalize(model)
	if m == "" {
		return modelPrice{}, "", false
	}
	if mp, ok := p.Models[m]; ok {
		return mp, m, true
	}
	for _, k := range p.keys {
		if strings.HasPrefix(m, k) {
			return p.Models[k], k, true
		}
	}
	fams := make([]string, 0, len(p.Families))
	for k := range p.Families {
		fams = append(fams, k)
	}
	sort.Strings(fams)
	for _, k := range fams {
		if strings.Contains(m, k) {
			return p.Families[k], k, true
		}
	}
	return modelPrice{}, "", false
}

// vendorOf names a model's vendor: as the table says, else from its name.
func vendorOf(mp modelPrice, key string) string {
	if mp.Vendor != "" {
		return mp.Vendor
	}
	switch {
	case strings.HasPrefix(key, "gpt"), strings.HasPrefix(key, "codex"), len(key) > 1 && key[0] == 'o' && key[1] >= '0' && key[1] <= '9':
		return "openai"
	case strings.HasPrefix(key, "gemini"):
		return "google"
	}
	return "anthropic"
}

// Priced reports whether a model has a price.
func (p *Pricing) Priced(model string) bool {
	_, _, ok := p.lookup(model)
	return ok
}

// Cost is a call's cost at list prices, in USD. ok is false when the model
// has no price (it's then counted, but never guessed).
func (p *Pricing) Cost(c Call) (float64, bool) {
	mp, key, ok := p.lookup(c.M)
	if !ok {
		return 0, false
	}
	vendor := vendorOf(mp, key)
	rules := p.Vendors[vendor]
	r := mp.rates
	// Prices that start on a date apply to calls from that day.
	for _, ch := range mp.Changes {
		if c.D >= ch.From {
			r = ch.rates
		}
	}
	// A long prompt pays the long price for the whole call. Unlisted long
	// cache-write prices scale with the input price.
	if mp.Long != nil && c.Prompt() > mp.Long.Above {
		lr := mp.Long.rates
		if lr.CacheWrite == nil && r.CacheWrite != nil && r.Input > 0 {
			v := *r.CacheWrite * lr.Input / r.Input
			lr.CacheWrite = &v
		}
		r = lr
	}
	w5, w1h := r.Input*1.25, r.Input*2
	switch vendor {
	case "anthropic":
		if rules.CacheWrite5m > 0 {
			w5 = r.Input * rules.CacheWrite5m
		}
		if rules.CacheWrite1h > 0 {
			w1h = r.Input * rules.CacheWrite1h
		}
	default:
		if r.CacheWrite != nil {
			w5 = *r.CacheWrite
		} else if rules.CacheWrite > 0 {
			w5 = r.Input * rules.CacheWrite
		} else {
			w5 = r.Input
		}
		w1h = w5
	}
	cost := (float64(c.In)*r.Input + float64(c.Out)*r.Output + float64(c.CR)*r.CacheRead +
		float64(c.CW5)*w5 + float64(c.CW1h)*w1h) / 1e6
	if c.Fast {
		f := rules.Fast
		if f == 0 {
			f = 2
		}
		cost *= f
	}
	return cost, true
}
