package fdl

// DocxXML 的具名颜色（text-color / background-color 的取值）和调色板顺
// 序（对拍 palette.rs）。粘贴时把剪贴板颜色吸附到最近的色板。

// Rgba 是一个颜色。
type Rgba struct {
	R, G, B uint8
	A       float32
}

func hexColor(v uint32) Rgba {
	return Rgba{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: 1}
}

func rgbaColor(r, g, b uint8, a float32) Rgba {
	return Rgba{R: r, G: g, B: b, A: a}
}

// Hues 是色相名（面板顺序）。
var Hues = []string{"gray", "red", "orange", "yellow", "green", "blue", "purple"}

// FontColors 是文字色板（第一个是默认色）。
var FontColors = []struct {
	Name *string
	Hex  uint32
}{
	{nil, 0x1f2329},
	{strPtr("gray"), 0x8f959e},
	{strPtr("red"), 0xd83931},
	{strPtr("orange"), 0xde7802},
	{strPtr("yellow"), 0xdc9b04},
	{strPtr("green"), 0x2ea121},
	{strPtr("blue"), 0x245bdb},
	{strPtr("purple"), 0x6425d0},
}

func strPtr(s string) *string { return &s }

// BGColors 是 A 面板的文字高亮色板：两行各 8 个（第一行以「无」开头）。
var BGColors = []*string{
	nil,
	strPtr("light-gray"), strPtr("light-red"), strPtr("light-orange"), strPtr("light-yellow"),
	strPtr("light-green"), strPtr("light-blue"), strPtr("light-purple"),
	strPtr("medium-gray"), strPtr("gray"), strPtr("red"), strPtr("orange"),
	strPtr("yellow"), strPtr("green"), strPtr("blue"), strPtr("purple"),
}

// CalloutBGColors 是高亮块填充色板（块背景调色板）。
var CalloutBGColors = []*string{
	nil,
	strPtr("light-gray"), strPtr("light-red"), strPtr("light-orange"), strPtr("light-yellow"),
	strPtr("light-green"), strPtr("light-blue"), strPtr("light-purple"),
	strPtr("medium-gray"), strPtr("gray"), strPtr("medium-red"), strPtr("medium-orange"),
	strPtr("medium-yellow"), strPtr("medium-green"), strPtr("medium-blue"), strPtr("medium-purple"),
}

// TextColor 返回文字色板里该颜色的值。
func TextColor(name string) (Rgba, bool) {
	for _, f := range FontColors {
		if f.Name != nil && *f.Name == name {
			return hexColor(f.Hex), true
		}
	}
	return Rgba{}, false
}

// BGColor 返回文字高亮色的实际颜色；medium-{hue}（旧版本写的）按第二行
// 色相读。
func BGColor(name string) (Rgba, bool) {
	switch name {
	case "light-gray":
		return hexColor(0xf2f3f5), true
	case "light-red":
		return hexColor(0xfbbfbc), true
	case "light-orange":
		return rgbaColor(254, 212, 164, 0.8), true
	case "light-yellow":
		return rgbaColor(255, 246, 122, 0.8), true
	case "light-green":
		return rgbaColor(183, 237, 177, 0.8), true
	case "light-blue":
		return rgbaColor(186, 206, 253, 0.7), true
	case "light-purple":
		return rgbaColor(205, 178, 250, 0.7), true
	case "medium-gray":
		return rgbaColor(222, 224, 227, 0.8), true
	case "gray":
		return hexColor(0xbbbfc4), true
	case "medium-red", "red":
		return hexColor(0xf76964), true
	case "medium-orange", "orange":
		return hexColor(0xffa53d), true
	case "medium-yellow", "yellow":
		return hexColor(0xffe928), true
	case "medium-green", "green":
		return hexColor(0x62d256), true
	case "medium-blue", "blue":
		return rgbaColor(78, 131, 253, 0.55), true
	case "medium-purple", "purple":
		return rgbaColor(147, 90, 246, 0.55), true
	}
	return Rgba{}, false
}

// BlockBG 返回块背景（块背景、表格单元格与高亮块填充）的实际颜色。
func BlockBG(name string) (Rgba, bool) {
	switch name {
	case "light-gray":
		return rgbaColor(245, 246, 247, 0.9), true
	case "light-red":
		return hexColor(0xfef1f1), true
	case "light-orange":
		return hexColor(0xfff5eb), true
	case "light-yellow":
		return hexColor(0xfefff0), true
	case "light-green":
		return hexColor(0xf0fbef), true
	case "light-blue":
		return hexColor(0xf0f4ff), true
	case "light-purple":
		return hexColor(0xf6f1fe), true
	case "medium-gray":
		return hexColor(0xf2f3f5), true
	case "gray":
		return hexColor(0xeff0f1), true
	case "medium-red", "red":
		return hexColor(0xfde2e2), true
	case "medium-orange", "orange":
		return hexColor(0xfeead2), true
	case "medium-yellow", "yellow":
		return hexColor(0xffffcc), true
	case "medium-green", "green":
		return hexColor(0xd9f5d6), true
	case "medium-blue", "blue":
		return hexColor(0xe1eaff), true
	case "medium-purple", "purple":
		return hexColor(0xece2fe), true
	}
	return Rgba{}, false
}

// CalloutBG 返回高亮块填充（块背景，浅灰除外）。
func CalloutBG(name string) (Rgba, bool) {
	if name == "light-gray" {
		return hexColor(0xf5f6f7), true
	}
	return BlockBG(name)
}

// CalloutBorder 返回高亮块边框颜色；旧的深色集合同样按色相名接受。
func CalloutBorder(name string) (Rgba, bool) {
	switch name {
	case "gray", "light-gray", "medium-gray", "neutral":
		return hexColor(0xeff0f1), true
	case "red", "light-red", "medium-red":
		return hexColor(0xfbbfbc), true
	case "orange", "light-orange", "medium-orange":
		return hexColor(0xfed4a4), true
	case "yellow", "light-yellow", "medium-yellow":
		return hexColor(0xfff67a), true
	case "green", "light-green", "medium-green":
		return hexColor(0xb7edb1), true
	case "blue", "light-blue", "medium-blue":
		return hexColor(0xbacefd), true
	case "purple", "light-purple", "medium-purple":
		return hexColor(0xcdb2fa), true
	}
	return Rgba{}, false
}
