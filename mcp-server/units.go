package main

import (
	"encoding/json"
	"strconv"
	"strings"
)

// 每张表先归一到基准单位（长度 m、质量 kg、时间 s），再做除法换算。
var unitTables = map[string]map[string]float64{
	"length": {
		"mm": 0.001,
		"cm": 0.01,
		"m":  1,
		"km": 1000,
		"in": 0.0254,
		"ft": 0.3048,
	},
	"mass": {
		"mg": 0.000001,
		"g":  0.001,
		"kg": 1,
		"t":  1000,
		"lb": 0.45359237,
	},
	"time": {
		"ms":  0.001,
		"s":   1,
		"min": 60,
		"h":   3600,
		"day": 86400,
	},
}

func unitInfo(unit string) (dimension string, rate float64, ok bool) {
	normalized := strings.ToLower(strings.TrimSpace(unit))
	for dimension, table := range unitTables {
		if rate, ok := table[normalized]; ok {
			return dimension, rate, true
		}
	}
	return "", 0, false
}

func unitConvert(raw json.RawMessage) (toolResult, *rpcError) {
	obj, rpcErr := decodeObject(raw)
	if rpcErr != nil {
		return toolResult{}, rpcErr
	}
	value, rpcErr := numberArg(obj, "value", true)
	if rpcErr != nil {
		return toolResult{}, rpcErr
	}
	from, rpcErr := stringArg(obj, "from", true)
	if rpcErr != nil {
		return toolResult{}, rpcErr
	}
	to, rpcErr := stringArg(obj, "to", true)
	if rpcErr != nil {
		return toolResult{}, rpcErr
	}

	fromDim, fromRate, ok := unitInfo(from)
	if !ok {
		return errorResult("不支持的单位：%s", from), nil
	}
	toDim, toRate, ok := unitInfo(to)
	if !ok {
		return errorResult("不支持的单位：%s", to), nil
	}
	if fromDim != toDim {
		return errorResult("不支持跨量纲换算：%s（%s）→ %s（%s）", from, fromDim, to, toDim), nil
	}

	converted := value * fromRate / toRate
	text := formatNumber(value) + " " + strings.TrimSpace(from) +
		" = " + formatNumber(converted) + " " + strings.TrimSpace(to)
	return textResult(text), nil
}

func formatNumber(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}
