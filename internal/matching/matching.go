// Package matching 负责把 bot 传来的模糊歌名与搜索结果做归一化比对。
//
// 存在的原因（实测结论）：YouTube Music 上游**永不返回空结果** —— 即使 query 是乱码，
// 也会返回 20 条无关歌曲。因此「搜到几条」不能作为「有没有搜到」的判据，
// 必须由本包给出 0~1 的相似度分数，让服务端（min_score）与 bot 侧自行决定阈值。
//
// 性能约束（R11）：MatchScore 会对每条搜索结果调用一次（一页最多 20 条），
// 处于 HTTP 请求的关键路径上。实现只用一次 rune 切片转换 + 双行 Levenshtein，
// 不使用正则（避免回溯），基准见 matching_test.go。
package matching

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

const (
	// fuzzyTokenMinLen 是参与词元级错拼匹配的最短长度。短词元（尤其是逐字切分的
	// 单个汉字）之间的 Levenshtein 相似度只有 0 或 1，模糊比对没有意义还会误命中。
	fuzzyTokenMinLen = 4
	// fuzzyTokenThreshold 是词元被判为「同一个词的错拼」所需的最低相似度。
	// 0.7 对 5 字母词允许 1 处编辑、对 8 字母词允许 2 处，
	// 同时把 "lemon" vs "tree"（0.2）这类不同词挡在外面。
	fuzzyTokenThreshold = 0.7
	// maxScoreRunes 限制参与打分的字符数。Levenshtein 是 O(n·m)，
	// 若不设上限，一条超长 query 就能吃掉大量 CPU（真实的 DoS 向量，
	// 因为 /search 的 query 完全由 bot 用户输入）。真实歌名远小于这个值。
	maxScoreRunes = 256
)

// BuildDisplayName 生成给用户看、也用于「按名字选歌」的全名。
//
// 格式固定为 "标题 - 歌手1, 歌手2"；无歌手时只有标题（不带尾随的 " - "）。
// bot 侧把这个字符串原样展示给用户，用户回复它时服务端按此比对，
// 所以格式一旦定下不能再变（见 docs/BOT-INTEGRATION.md §8 契约承诺）。
func BuildDisplayName(title string, artists []string) string {
	title = strings.TrimSpace(title)

	named := make([]string, 0, len(artists))
	for _, a := range artists {
		if a = strings.TrimSpace(a); a != "" {
			named = append(named, a)
		}
	}
	if len(named) == 0 {
		return title
	}
	joined := strings.Join(named, ", ")
	if title == "" {
		return joined
	}
	return title + " - " + joined
}

// Normalize 把字符串收敛成可比对的形式：
// NFKC（全角→半角、兼容字符展开）→ 小写 → 标点/符号转空格 → 压缩空白。
//
// 例："　Ｌｅｍｏｎ（Ｏｆｆｉｃｉａｌ）" → "lemon official"
func Normalize(s string) string {
	if s == "" {
		return ""
	}
	s = norm.NFKC.String(s)

	var b strings.Builder
	b.Grow(len(s))
	pendingSpace := false
	wrote := false

	for _, r := range s {
		switch {
		case unicode.IsSpace(r), isSeparator(r):
			pendingSpace = wrote
		case unicode.IsLetter(r), unicode.IsDigit(r):
			if pendingSpace {
				b.WriteRune(' ')
				pendingSpace = false
			}
			b.WriteRune(unicode.ToLower(r))
			wrote = true
		default:
			// 其余（标点、符号、控制字符）视为分隔符丢弃。
			pendingSpace = wrote
		}
	}
	return b.String()
}

// isSeparator 判断是否为应当视作词边界的标点/符号。
func isSeparator(r rune) bool {
	return unicode.IsPunct(r) || unicode.IsSymbol(r)
}

// Tokenize 把**已归一化**的字符串切成词元：
// 拉丁/数字按空白分词，CJK 表意文字与假名按单字切分。
//
// 逐字切 CJK 的理由：中文/日文歌名之间没有空格（"周杰伦晴天"），
// 按空白分词会得到一个巨大的词元，导致部分匹配完全失效。
func Tokenize(normalized string) []string {
	if normalized == "" {
		return nil
	}

	runes := []rune(normalized)
	tokens := make([]string, 0, len(runes)/2+1)
	start := -1

	flush := func(end int) {
		if start >= 0 {
			tokens = append(tokens, string(runes[start:end]))
			start = -1
		}
	}

	for i, r := range runes {
		switch {
		case r == ' ':
			flush(i)
		case isCJK(r):
			flush(i)
			tokens = append(tokens, string(r))
		default:
			if start < 0 {
				start = i
			}
		}
	}
	flush(len(runes))
	return tokens
}

// isCJK 判断是否为需要逐字切分的东亚文字。
func isCJK(r rune) bool {
	switch {
	case r >= 0x3040 && r <= 0x30FF: // 平假名 / 片假名
		return true
	case r >= 0x3400 && r <= 0x4DBF: // 扩展 A
		return true
	case r >= 0x4E00 && r <= 0x9FFF: // 基本区
		return true
	case r >= 0xF900 && r <= 0xFAFF: // 兼容表意文字
		return true
	case r >= 0xAC00 && r <= 0xD7AF: // 韩文音节
		return true
	case r >= 0x20000 && r <= 0x2FA1F: // 扩展 B~F + 兼容补充
		return true
	}
	return false
}

// MatchScore 返回 query 与 target（通常是 display_name）的相似度，范围 0~1。
//
// 取三种策略的最大值，因为 bot 用户的输入形态差异很大：
//  1. 整串相似度（Levenshtein）—— 应付错字、多字少字；
//  2. 子串包含 —— 应付「只打了歌名、没打歌手」；
//  3. 词元覆盖率 —— 应付词序颠倒（"周杰伦 晴天" vs "晴天 - 周杰倫"）与简繁部分差异。
//
// 归一化后完全相同返回 1.0；任一侧为空返回 0。
func MatchScore(query, target string) float64 {
	nq := Normalize(query)
	nt := Normalize(target)
	if nq == "" || nt == "" {
		return 0
	}
	if nq == nt {
		return 1
	}

	// 截断后再比对：超长输入的尾部对「是不是这首歌」没有信息量，
	// 但会让代价平方级上升。相等判断放在截断之前，保证完全相同仍是 1.0。
	nq = truncateRunes(nq, maxScoreRunes)
	nt = truncateRunes(nt, maxScoreRunes)

	qr := []rune(nq)
	tr := []rune(nt)

	best := similarity(qr, tr)

	// query 整串出现在 target 里：至少 0.75，再按它覆盖 target 的比例上浮。
	// 这样 "lemon" 对 "lemon" 短标题的得分高于对超长标题的得分。
	if strings.Contains(nt, nq) {
		ratio := float64(len(qr)) / float64(len(tr))
		if score := 0.75 + 0.25*ratio; score > best {
			best = score
		}
	}

	if score := tokenScore(nq, nt); score > best {
		best = score
	}
	return best
}

// tokenScore 以「query 词元被覆盖的比例」为主，辅以 target 的紧凑度。
//
// 主项回答「用户要的东西是否都在」，辅项让 "lemon" 对
// "Lemon - Kenshi Yonezu"（0.90）低于对 "Lemon"（整串相同，1.0），
// 避免残缺 query 也能拿满分而失去排序区分度。
func tokenScore(nq, nt string) float64 {
	queryTokens := Tokenize(nq)
	targetTokens := Tokenize(nt)
	if len(queryTokens) == 0 || len(targetTokens) == 0 {
		return 0
	}

	// 用计数而非集合：重复词元（如 "la la la"）不应被一个 target 词元反复命中。
	remaining := make(map[string]int, len(targetTokens))
	for _, t := range targetTokens {
		remaining[t]++
	}

	// 第一轮：精确命中。未命中的词元推迟到第二轮，
	// 否则模糊匹配可能抢走某个精确命中本该消耗的 target 词元。
	weight := 0.0
	consumed := 0
	var unmatched []string
	for _, q := range queryTokens {
		if remaining[q] > 0 {
			remaining[q]--
			weight++
			consumed++
			continue
		}
		if utf8.RuneCountInString(q) >= fuzzyTokenMinLen {
			unmatched = append(unmatched, q)
		}
	}

	// 第二轮：错拼容忍。缺了这一轮，"lemmon" 这种手误 query 对完整
	// display_name 只剩整串 Levenshtein 的 0.26 分，与乱码 query 无从区分，
	// min_score 也就失去了意义（R3 要求按「近似名字」搜索）。
	// 命中权重取实际相似度而非 1.0，保证精确命中始终排在错拼命中之前。
	for _, q := range unmatched {
		qr := []rune(q)
		bestIdx, bestSim := -1, 0.0
		for i, t := range targetTokens {
			if remaining[t] <= 0 {
				continue
			}
			tn := utf8.RuneCountInString(t)
			if tn < fuzzyTokenMinLen {
				continue
			}
			// 长度差就是 Levenshtein 距离的下界，据此可算出相似度上界。
			// 上界都够不到阈值/当前最优就不必算矩阵了（纯剪枝，不改变结果）。
			if upper := similarityUpperBound(len(qr), tn); upper < fuzzyTokenThreshold || upper <= bestSim {
				continue
			}
			// 严格大于：同分取靠前的词元，保证结果不受 map 遍历顺序影响。
			if sim := similarity(qr, []rune(t)); sim > bestSim {
				bestIdx, bestSim = i, sim
			}
		}
		if bestIdx >= 0 && bestSim >= fuzzyTokenThreshold {
			remaining[targetTokens[bestIdx]]--
			weight += bestSim
			consumed++
		}
	}

	if consumed == 0 {
		return 0
	}

	queryCoverage := weight / float64(len(queryTokens))
	targetCoverage := float64(consumed) / float64(len(targetTokens))
	return queryCoverage * (0.85 + 0.15*targetCoverage)
}

// similarity 由 Levenshtein 距离换算的相似度：1 - dist/max(len)。
func similarity(a, b []rune) float64 {
	longest := len(a)
	if len(b) > longest {
		longest = len(b)
	}
	if longest == 0 {
		return 0
	}
	dist := levenshtein(a, b)
	if dist >= longest {
		return 0
	}
	return 1 - float64(dist)/float64(longest)
}

// similarityUpperBound 给出仅凭两侧长度可知的相似度上界。
// 依据：Levenshtein 距离 >= |la-lb|，故相似度 <= 1 - |la-lb|/max(la,lb)。
func similarityUpperBound(la, lb int) float64 {
	if la == 0 || lb == 0 {
		return 0
	}
	longest, shortest := la, lb
	if lb > la {
		longest, shortest = lb, la
	}
	return float64(shortest) / float64(longest)
}

// truncateRunes 按字符数截断，不会切坏多字节字符。
func truncateRunes(s string, limit int) string {
	if utf8.RuneCountInString(s) <= limit {
		return s
	}
	count := 0
	for i := range s {
		if count == limit {
			return s[:i]
		}
		count++
	}
	return s
}

// levenshtein 是双行滚动实现：O(len(a)*len(b)) 时间、O(min(len)) 空间，无逐轮分配。
func levenshtein(a, b []rune) int {
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}
	// 让 b 成为较短的一侧，把滚动数组的宽度压到 min(len)。
	if len(b) > len(a) {
		a, b = b, a
	}

	prev := make([]int, len(b)+1)
	curr := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}

	for i := 1; i <= len(a); i++ {
		curr[0] = i
		ai := a[i-1]
		for j := 1; j <= len(b); j++ {
			cost := 1
			if ai == b[j-1] {
				cost = 0
			}
			del := prev[j] + 1
			ins := curr[j-1] + 1
			sub := prev[j-1] + cost

			best := del
			if ins < best {
				best = ins
			}
			if sub < best {
				best = sub
			}
			curr[j] = best
		}
		prev, curr = curr, prev
	}
	return prev[len(b)]
}
