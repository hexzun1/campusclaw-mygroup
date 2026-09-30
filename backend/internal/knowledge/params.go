package knowledge

import (
	"fmt"
	"strconv"
)

// Strategy selects how a body text is cut into chunks (spec: 切分策略).
type Strategy string

const (
	// StrategyAuto is the default: 800 characters per chunk, 80 shared.
	StrategyAuto Strategy = "auto"
	// StrategyCustom uses the caller's chunk size and overlap percentage.
	StrategyCustom Strategy = "custom"
	// StrategyHierarchy splits on Markdown headings.
	StrategyHierarchy Strategy = "hierarchy"
)

// Parameter names accepted from an upload form or a reindex body. They are
// constants so the HTTP layer, the tests and the README agree on spelling.
const (
	ParamStrategy           = "strategy"
	ParamChunkSize          = "chunk_size"
	ParamOverlapPercent     = "overlap_percent"
	ParamRemoveURL          = "remove_url"
	ParamRemoveEmail        = "remove_email"
	ParamCollapseWhitespace = "collapse_whitespace"
)

// Bounds from spec: 切分策略 — custom chunks are 100-2000 characters with
// 0-50 percent overlap.
const (
	MinChunkSize      = 100
	MaxChunkSize      = 2000
	MinOverlapPercent = 0
	MaxOverlapPercent = 50
)

// DefaultOverlapPercent is the auto strategy's overlap expressed as a
// percentage: 80 is 10 percent of 800.
const DefaultOverlapPercent = AutoOverlap * 100 / AutoSize

// ChunkParams is the validated form of the chunking parameters a client may
// send. Booleans control preprocessing, which only ever affects the stored
// chunk text and never the body (design.md Decision 5).
type ChunkParams struct {
	Strategy           Strategy
	ChunkSize          int
	OverlapPercent     int
	RemoveURL          bool
	RemoveEmail        bool
	CollapseWhitespace bool
}

// DefaultParams is the auto strategy with its fixed window: what an upload that
// sends no chunking parameters gets.
func DefaultParams() ChunkParams {
	return ChunkParams{
		Strategy:       StrategyAuto,
		ChunkSize:      AutoSize,
		OverlapPercent: DefaultOverlapPercent,
	}
}

// Window returns the character window this parameter set cuts with. auto and
// hierarchy always use the fixed auto window; custom uses the caller's size and
// percentage.
func (p ChunkParams) Window() (size, overlap int) {
	if p.Strategy != StrategyCustom {
		return AutoSize, AutoOverlap
	}
	size = p.ChunkSize
	if size <= 0 {
		size = AutoSize
	}
	return size, OverlapFor(size, p.OverlapPercent)
}

// ParamsJSON renders the parameters for the knowledge_entries.chunk_params
// column, which shows what the most recent chunking run used.
func (p ChunkParams) ParamsJSON() []byte {
	return []byte(fmt.Sprintf(
		`{"strategy":%q,"chunk_size":%d,"overlap_percent":%d,"remove_url":%t,"remove_email":%t,"collapse_whitespace":%t}`,
		p.Strategy, p.ChunkSize, p.OverlapPercent, p.RemoveURL, p.RemoveEmail, p.CollapseWhitespace))
}

// ParseParams validates raw parameter values, where a missing key means "not
// supplied" and keeps its default. An empty string is not the same as a missing
// key: the HTTP layer builds this map from the keys the client actually sent,
// so `remove_url=` is an invalid boolean rather than an absent one.
//
// auto and hierarchy have a fixed window, so combining them with chunk_size or
// overlap_percent is rejected instead of silently ignored.
func ParseParams(raw map[string]string) (ChunkParams, error) {
	params := DefaultParams()

	if v, ok := raw[ParamStrategy]; ok {
		switch Strategy(v) {
		case StrategyAuto, StrategyCustom, StrategyHierarchy:
			params.Strategy = Strategy(v)
		default:
			return ChunkParams{}, fmt.Errorf("%s must be one of auto, custom, hierarchy", ParamStrategy)
		}
	}

	size, hasSize, err := optionalInt(raw, ParamChunkSize, MinChunkSize, MaxChunkSize)
	if err != nil {
		return ChunkParams{}, err
	}
	percent, hasPercent, err := optionalInt(raw, ParamOverlapPercent, MinOverlapPercent, MaxOverlapPercent)
	if err != nil {
		return ChunkParams{}, err
	}

	if params.Strategy != StrategyCustom {
		if hasSize || hasPercent {
			return ChunkParams{}, fmt.Errorf(
				"%s and %s are only accepted with %s=%s",
				ParamChunkSize, ParamOverlapPercent, ParamStrategy, StrategyCustom)
		}
	} else {
		if hasSize {
			params.ChunkSize = size
		}
		if hasPercent {
			params.OverlapPercent = percent
		}
	}

	for _, b := range []struct {
		name  string
		value *bool
	}{
		{ParamRemoveURL, &params.RemoveURL},
		{ParamRemoveEmail, &params.RemoveEmail},
		{ParamCollapseWhitespace, &params.CollapseWhitespace},
	} {
		parsed, err := optionalBool(raw, b.name)
		if err != nil {
			return ChunkParams{}, err
		}
		*b.value = parsed
	}

	return params, nil
}

func optionalInt(raw map[string]string, name string, minValue, maxValue int) (value int, present bool, err error) {
	v, ok := raw[name]
	if !ok {
		return 0, false, nil
	}
	n, convErr := strconv.Atoi(v)
	if convErr != nil || n < minValue || n > maxValue {
		return 0, true, fmt.Errorf("%s must be an integer between %d and %d", name, minValue, maxValue)
	}
	return n, true, nil
}

// optionalBool accepts exactly "true" and "false"; anything else — including an
// empty value — is an error, per spec: 切分策略.
func optionalBool(raw map[string]string, name string) (bool, error) {
	v, ok := raw[name]
	if !ok {
		return false, nil
	}
	switch v {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("%s must be true or false", name)
	}
}
