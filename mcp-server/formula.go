package main

import (
	"encoding/json"
	"strings"
)

type formulaTopic struct {
	name    string
	keyword []string
	formula string
	idea    string
}

// 主题表按顺序匹配：更具体的主题排在前面，避免「功率」抢在「电功率」之前命中。
var formulaTopics = []formulaTopic{
	{
		name:    "勾股定理",
		keyword: []string{"勾股定理", "毕达哥拉斯"},
		formula: "a² + b² = c²（c 为斜边）",
		idea:    "先确认哪条边是斜边；把已知的两条边代入，求未知边，最后检查结果是否为正数并带单位。",
	},
	{
		name:    "一元二次方程",
		keyword: []string{"一元二次方程", "二次方程", "求根公式"},
		formula: "ax² + bx + c = 0（a≠0），x = (-b ± √(b²-4ac)) / (2a)",
		idea:    "先化成一般式并写出 a、b、c；再计算判别式 b²-4ac，用它判断根的个数，最后代入求根公式。",
	},
	{
		name:    "完全平方公式",
		keyword: []string{"完全平方"},
		formula: "(a ± b)² = a² ± 2ab + b²",
		idea:    "观察式子首尾是否为平方项、中间项是否为两数乘积的两倍，再决定用哪一个符号。",
	},
	{
		name:    "平方差公式",
		keyword: []string{"平方差"},
		formula: "a² - b² = (a + b)(a - b)",
		idea:    "确认两项都能写成平方且中间是减号，再分解为两数之和与两数之差。",
	},
	{
		name:    "三角函数",
		keyword: []string{"三角函数", "正弦", "余弦", "正切", "sin", "cos", "tan"},
		formula: "sin²θ + cos²θ = 1，tanθ = sinθ / cosθ",
		idea:    "先画图确定角的对边、邻边与斜边；再按定义选择正弦、余弦或正切，注意角的范围与符号。",
	},
	{
		name:    "等差数列",
		keyword: []string{"等差数列"},
		formula: "aₙ = a₁ + (n-1)d，Sₙ = n(a₁ + aₙ)/2",
		idea:    "先确定首项 a₁ 与公差 d；求第几项用通项公式，求和用前 n 项和公式。",
	},
	{
		name:    "等比数列",
		keyword: []string{"等比数列"},
		formula: "aₙ = a₁·qⁿ⁻¹，Sₙ = a₁(1-qⁿ)/(1-q)（q≠1）",
		idea:    "先确定首项与公比 q；注意 q=1 时求和公式不同，q 为负数时留意项的符号。",
	},
	{
		name:    "对数运算",
		keyword: []string{"对数", "log"},
		formula: "logₐ(xy) = logₐx + logₐy，logₐ(xⁿ) = n·logₐx",
		idea:    "先检查真数大于 0、底数大于 0 且不等于 1；再把乘除与幂运算拆成加减与倍数。",
	},
	{
		name:    "指数运算",
		keyword: []string{"指数", "幂运算"},
		formula: "aᵐ·aⁿ = aᵐ⁺ⁿ，(aᵐ)ⁿ = aᵐⁿ，a⁻ⁿ = 1/aⁿ",
		idea:    "同底数相乘指数相加，幂的幂指数相乘；负指数先转成正指数的倒数。",
	},
	{
		name:    "圆的周长与面积",
		keyword: []string{"圆的面积", "圆面积", "圆的周长", "圆周长"},
		formula: "S = πr²，C = 2πr",
		idea:    "先分清给的是半径还是直径；求面积用半径的平方，求周长用直径乘 π。",
	},
	{
		name:    "三角形面积",
		keyword: []string{"三角形面积"},
		formula: "S = ½ × 底 × 高",
		idea:    "底与高必须互相垂直；若给的是三边，可先用海伦公式或作高求面积。",
	},
	{
		name:    "概率",
		keyword: []string{"概率"},
		formula: "P(A) = 有利结果数 / 全部等可能结果数",
		idea:    "先确认所有结果等可能，再数清有利结果与总结果；互斥事件概率相加，独立事件概率相乘。",
	},
	{
		name:    "平均数",
		keyword: []string{"平均数", "平均值"},
		formula: "x̄ = (x₁ + x₂ + … + xₙ) / n",
		idea:    "先求和再除以个数；若数据带有权重，要用加权平均数。",
	},
	{
		name:    "速度",
		keyword: []string{"速度"},
		formula: "v = s / t",
		idea:    "先统一路程与时间的单位；平均速度等于总路程除以总时间，不是各段速度的平均。",
	},
	{
		name:    "密度",
		keyword: []string{"密度"},
		formula: "ρ = m / V",
		idea:    "先确认质量与体积的单位；单位换算时体积按长度单位的三次方换算。",
	},
	{
		name:    "牛顿第二定律",
		keyword: []string{"牛顿第二定律", "F=ma"},
		formula: "F = ma",
		idea:    "先受力分析，取合外力方向为正；质量用千克、加速度用米每二次方秒，结果才是牛顿。",
	},
	{
		name:    "动能定理",
		keyword: []string{"动能定理", "动能"},
		formula: "W合 = ΔEₖ = ½mv₂² - ½mv₁²",
		idea:    "先算所有外力做的总功，再等于动能变化；注意功与动能都是标量，但要带正负号。",
	},
	{
		name:    "机械能守恒",
		keyword: []string{"机械能守恒"},
		formula: "Eₖ₁ + Eₚ₁ = Eₖ₂ + Eₚ₂",
		idea:    "先判断是否只有重力或弹力做功；再选好零势能面，分别写出初末状态的动能与势能。",
	},
	{
		name:    "动量守恒",
		keyword: []string{"动量守恒", "动量"},
		formula: "m₁v₁ + m₂v₂ = m₁v₁' + m₂v₂'",
		idea:    "先规定正方向；只考虑系统所受合外力为零的过程，碰撞前后动量矢量和保持不变。",
	},
	{
		name:    "欧姆定律",
		keyword: []string{"欧姆定律"},
		formula: "U = IR",
		idea:    "先明确电压、电流、电阻是针对同一段电路；串联分压、并联分流，先画等效电路再代入。",
	},
	{
		name:    "电功率",
		keyword: []string{"电功率"},
		formula: "P = UI = I²R = U²/R",
		idea:    "先确认是纯电阻电路；已知电压与电流用 UI，已知电流与电阻用 I²R，已知电压与电阻用 U²/R。",
	},
	{
		name:    "功率",
		keyword: []string{"功率"},
		formula: "P = W / t = Fv",
		idea:    "功率表示做功的快慢；先确认是平均功率还是瞬时功率，再选择对应的公式。",
	},
	{
		name:    "浮力",
		keyword: []string{"浮力", "阿基米德"},
		formula: "F浮 = ρ液 g V排 = G排",
		idea:    "先判断物体是否浸没；V排 是排开液体的体积，不是物体的总体积，注意单位换算。",
	},
	{
		name:    "压强",
		keyword: []string{"压强"},
		formula: "p = F / S，液体内部 p = ρgh",
		idea:    "固体压强先找压力与受力面积；液体压强只看液体密度与深度，与容器形状无关。",
	},
	{
		name:    "圆周运动",
		keyword: []string{"圆周运动", "向心力"},
		formula: "F向 = mv²/r = mω²r",
		idea:    "先找圆心与半径；向心力是效果力，由实际的合力提供，受力分析时不要重复添加。",
	},
	{
		name:    "万有引力",
		keyword: []string{"万有引力"},
		formula: "F = G·m₁m₂ / r²",
		idea:    "r 是两质心间的距离；天体运动中常把万有引力作为向心力，联立求解周期或线速度。",
	},
	{
		name:    "理想气体状态方程",
		keyword: []string{"理想气体", "气体状态方程"},
		formula: "pV = nRT",
		idea:    "温度必须用开尔文；先确定初末状态不变的量，再选择等温、等容或等压过程简化。",
	},
}

func formulaHint(raw json.RawMessage) (toolResult, *rpcError) {
	obj, rpcErr := decodeObject(raw)
	if rpcErr != nil {
		return toolResult{}, rpcErr
	}
	topic, rpcErr := stringArg(obj, "topic", true)
	if rpcErr != nil {
		return toolResult{}, rpcErr
	}
	level, rpcErr := stringArg(obj, "level", false)
	if rpcErr != nil {
		return toolResult{}, rpcErr
	}

	query := strings.TrimSpace(topic)
	for _, t := range formulaTopics {
		for _, keyword := range t.keyword {
			if strings.Contains(query, keyword) {
				var b strings.Builder
				b.WriteString("【")
				b.WriteString(t.name)
				b.WriteString("】\n公式：")
				b.WriteString(t.formula)
				b.WriteString("\n思路：")
				b.WriteString(t.idea)
				if level == "详细" || strings.EqualFold(level, "detailed") {
					b.WriteString("\n提示：先列出已知量与未知量，再选择公式，代入后检查单位与结果是否合理。")
				}
				return textResult(b.String()), nil
			}
		}
	}
	return textResult("暂无该主题的提示"), nil
}
