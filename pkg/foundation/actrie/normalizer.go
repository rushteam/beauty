package actrie

import "unicode"

// 本文件提供内置的 rune 级归一化函数,配合 WithNormalizer 使用,用于对抗敏感词绕过。
// 约定:归一化函数返回 0 表示"跳过该字符"(不参与匹配)。均为纯标准库实现。

// FoldCase 大小写折叠(转小写),使匹配大小写不敏感。
//
//	actrie.New(actrie.WithNormalizer(actrie.FoldCase()))
func FoldCase() func(rune) rune {
	return unicode.ToLower
}

// Keep 只保留满足 pred 的字符,其余跳过(返回 0)。用于剥离 * - 空格等插入型干扰符。
//
//	// 只保留字母/数字,可命中 "F*u*c*k"、"f u c k"
//	actrie.New(actrie.WithNormalizer(actrie.Chain(
//	    actrie.Keep(func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }),
//	    actrie.FoldCase(),
//	)))
func Keep(pred func(rune) bool) func(rune) rune {
	return func(r rune) rune {
		if pred(r) {
			return r
		}
		return 0
	}
}

// Chain 串联多个归一化函数,按顺序依次施加;任一步返回 0(跳过)则整体跳过。
func Chain(fns ...func(rune) rune) func(rune) rune {
	return func(r rune) rune {
		for _, fn := range fns {
			r = fn(r)
			if r == 0 {
				return 0
			}
		}
		return r
	}
}

// VisualMap 视觉混淆归一化:把常见同形/相似字符映射回 ASCII 并转小写。覆盖:
//   - 数字/符号伪装:0→o 1→l 3→e 4→a 5→s 7→t 8→b @→a $→s
//   - 拉丁变音字母:à-å→a è-ë→e ì-ï→i ò-ö/ø→o ù-ü→u ñ→n ç→c
//   - 西里尔同形字:а→a е→e о→o р→p с→c у→y х→x к→k м→m т→t
//   - 全角字母/数字:Ａ-Ｚ ａ-ｚ ０-９ → 半角
//
// 位置保持:均为 1→1 映射,命中位置精确对应原文。
func VisualMap() func(rune) rune {
	return func(r rune) rune {
		if v, ok := visualTable[r]; ok {
			return v
		}
		// 全角字母/数字 → 半角(U+FF01..U+FF5E 与 ASCII 相差 0xFEE0)
		if r >= 0xFF01 && r <= 0xFF5E {
			return unicode.ToLower(r - 0xFEE0)
		}
		return unicode.ToLower(r)
	}
}

// visualTable 视觉混淆映射表(目标均为小写 ASCII)。
var visualTable = map[rune]rune{
	// 数字/符号伪装
	'0': 'o', '1': 'l', '3': 'e', '4': 'a', '5': 's', '7': 't', '8': 'b',
	'@': 'a', '$': 's',
	// 拉丁变音
	'à': 'a', 'á': 'a', 'â': 'a', 'ã': 'a', 'ä': 'a', 'å': 'a',
	'è': 'e', 'é': 'e', 'ê': 'e', 'ë': 'e',
	'ì': 'i', 'í': 'i', 'î': 'i', 'ï': 'i',
	'ò': 'o', 'ó': 'o', 'ô': 'o', 'õ': 'o', 'ö': 'o', 'ø': 'o',
	'ù': 'u', 'ú': 'u', 'û': 'u', 'ü': 'u',
	'ñ': 'n', 'ç': 'c', 'ý': 'y', 'ÿ': 'y',
	'À': 'a', 'Á': 'a', 'Â': 'a', 'Ã': 'a', 'Ä': 'a', 'Å': 'a',
	'È': 'e', 'É': 'e', 'Ê': 'e', 'Ë': 'e',
	'Ì': 'i', 'Í': 'i', 'Î': 'i', 'Ï': 'i',
	'Ò': 'o', 'Ó': 'o', 'Ô': 'o', 'Õ': 'o', 'Ö': 'o', 'Ø': 'o',
	'Ù': 'u', 'Ú': 'u', 'Û': 'u', 'Ü': 'u',
	'Ñ': 'n', 'Ç': 'c',
	// 西里尔同形字(小写)
	'а': 'a', 'е': 'e', 'о': 'o', 'р': 'p', 'с': 'c', 'у': 'y', 'х': 'x',
	'к': 'k', 'м': 'm', 'т': 't', 'в': 'b', 'н': 'h', 'і': 'i', 'ѕ': 's',
	// 西里尔同形字(大写)
	'А': 'a', 'Е': 'e', 'О': 'o', 'Р': 'p', 'С': 'c', 'У': 'y', 'Х': 'x',
	'К': 'k', 'М': 'm', 'Т': 't', 'В': 'b', 'Н': 'h', 'І': 'i',
}
