package matching

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestBuildDisplayName(t *testing.T) {
	cases := []struct {
		name    string
		title   string
		artists []string
		want    string
	}{
		{"单歌手", "Lemon", []string{"Kenshi Yonezu"}, "Lemon - Kenshi Yonezu"},
		{"多歌手逗号分隔", "KICK BACK", []string{"Kenshi Yonezu", "Chainsaw Man"}, "KICK BACK - Kenshi Yonezu, Chainsaw Man"},
		{"无歌手不带连字符", "Instrumental", nil, "Instrumental"},
		{"空歌手数组", "Instrumental", []string{}, "Instrumental"},
		{"歌手全是空串", "Instrumental", []string{"", "  "}, "Instrumental"},
		{"过滤空歌手", "晴天", []string{"周杰倫", ""}, "晴天 - 周杰倫"},
		{"去首尾空白", "  晴天  ", []string{"  周杰倫  "}, "晴天 - 周杰倫"},
		{"无标题只有歌手", "", []string{"周杰倫"}, "周杰倫"},
		{"全空", "", nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := BuildDisplayName(tc.title, tc.artists); got != tc.want {
				t.Errorf("BuildDisplayName(%q, %v) = %q, 期望 %q", tc.title, tc.artists, got, tc.want)
			}
		})
	}
}

func TestNormalize(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"大小写", "LEMON", "lemon"},
		{"压缩空白", "  lemon   yonezu  ", "lemon yonezu"},
		{"制表换行", "lemon\tkenshi\nyonezu", "lemon kenshi yonezu"},
		{"全角转半角", "Ｌｅｍｏｎ", "lemon"},
		{"全角空格", "晴天　周杰倫", "晴天 周杰倫"},
		{"括号变分隔", "Lemon (Official Video)", "lemon official video"},
		{"连字符变分隔", "Lemon - Kenshi Yonezu", "lemon kenshi yonezu"},
		{"中文标点", "晴天（钢琴版）", "晴天 钢琴版"},
		{"保留数字", "Song 2", "song 2"},
		{"emoji 视作分隔", "Lemon 🍋 Yonezu", "lemon yonezu"},
		{"纯标点", "!!!---***", ""},
		{"空串", "", ""},
		{"仅空白", "   ", ""},
		{"无尾随空格", "lemon!!!", "lemon"},
		{"无前导空格", "!!!lemon", "lemon"},
		{"CJK 不加空格", "周杰倫晴天", "周杰倫晴天"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Normalize(tc.input); got != tc.want {
				t.Errorf("Normalize(%q) = %q, 期望 %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestTokenize(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  []string
	}{
		{"拉丁按词", "lemon kenshi yonezu", []string{"lemon", "kenshi", "yonezu"}},
		{"单词", "lemon", []string{"lemon"}},
		{"中文逐字", "晴天", []string{"晴", "天"}},
		{"中文含空格", "晴天 周杰倫", []string{"晴", "天", "周", "杰", "倫"}},
		{"中文无空格", "周杰倫晴天", []string{"周", "杰", "倫", "晴", "天"}},
		{"日文假名逐字", "レモン", []string{"レ", "モ", "ン"}},
		{"日文汉字混假名", "米津玄師", []string{"米", "津", "玄", "師"}},
		{"中英混合", "晴天 remix", []string{"晴", "天", "remix"}},
		{"英中紧邻", "lemon晴天", []string{"lemon", "晴", "天"}},
		{"数字", "song 2", []string{"song", "2"}},
		{"空串", "", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Tokenize(tc.input)
			if len(got) != len(tc.want) {
				t.Fatalf("Tokenize(%q) = %v, 期望 %v", tc.input, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("Tokenize(%q)[%d] = %q, 期望 %q", tc.input, i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestMatchScoreExactIsOne(t *testing.T) {
	cases := []struct{ query, target string }{
		{"Lemon - Kenshi Yonezu", "Lemon - Kenshi Yonezu"},
		{"lemon - kenshi yonezu", "Lemon - Kenshi Yonezu"}, // 仅大小写差异
		{"晴天 - 周杰倫", "晴天 - 周杰倫"},
		{"  Lemon - Kenshi Yonezu  ", "Lemon - Kenshi Yonezu"}, // 仅空白差异
		{"Ｌｅｍｏｎ", "Lemon"},                                     // 仅全角差异
	}
	for _, tc := range cases {
		if got := MatchScore(tc.query, tc.target); got != 1 {
			t.Errorf("MatchScore(%q, %q) = %v, 期望 1.0", tc.query, tc.target, got)
		}
	}
}

func TestMatchScoreEmptyIsZero(t *testing.T) {
	cases := []struct{ query, target string }{
		{"", "Lemon"},
		{"Lemon", ""},
		{"", ""},
		{"!!!", "Lemon"}, // 归一化后为空
		{"Lemon", "***"}, // 归一化后为空
		{"   ", "Lemon"},
	}
	for _, tc := range cases {
		if got := MatchScore(tc.query, tc.target); got != 0 {
			t.Errorf("MatchScore(%q, %q) = %v, 期望 0", tc.query, tc.target, got)
		}
	}
}

func TestMatchScoreRange(t *testing.T) {
	queries := []string{"lemon", "晴天 周杰伦", "zzqq xxww", "米津玄師 レモン", "a"}
	targets := []string{"Lemon - Kenshi Yonezu", "晴天 - 周杰倫", "Some Random Song - Nobody", ""}
	for _, q := range queries {
		for _, tgt := range targets {
			got := MatchScore(q, tgt)
			if got < 0 || got > 1 {
				t.Errorf("MatchScore(%q, %q) = %v，超出 [0,1]", q, tgt, got)
			}
		}
	}
}

// 部分 query（只打歌名不打歌手）必须拿到高分，这是 bot 最常见的用法。
func TestMatchScorePartialQueryScoresHigh(t *testing.T) {
	cases := []struct {
		query  string
		target string
		min    float64
	}{
		{"lemon", "Lemon - Kenshi Yonezu", 0.7},
		{"kenshi yonezu", "Lemon - Kenshi Yonezu", 0.7},
		{"lemon yonezu", "Lemon - Kenshi Yonezu", 0.7},
		{"yonezu lemon", "Lemon - Kenshi Yonezu", 0.7}, // 词序颠倒
		{"kick back", "KICK BACK - Kenshi Yonezu", 0.7},
		{"晴天", "晴天 - 周杰倫", 0.7},
		{"周杰倫 晴天", "晴天 - 周杰倫", 0.7}, // 词序颠倒 + 繁体
		{"レモン", "レモン - 米津玄師", 0.7},
	}
	for _, tc := range cases {
		t.Run(tc.query, func(t *testing.T) {
			got := MatchScore(tc.query, tc.target)
			if got < tc.min {
				t.Errorf("MatchScore(%q, %q) = %.4f, 期望 >= %.2f", tc.query, tc.target, got, tc.min)
			}
			t.Logf("MatchScore(%q, %q) = %.4f", tc.query, tc.target, got)
		})
	}
}

// 乱码/无关 query 必须拿低分，否则 min_score 无法把垃圾结果滤掉（R6 的实现基础）。
func TestMatchScoreIrrelevantScoresLow(t *testing.T) {
	const maxScore = 0.35
	cases := []struct{ query, target string }{
		{"zzqqxxwweeyy nonexistent song 9182", "Lemon - Kenshi Yonezu"},
		{"zzqqxxwweeyy nonexistent song 9182", "晴天 - 周杰倫"},
		{"完全无关的中文查询词", "Lemon - Kenshi Yonezu"},
		{"asdfghjkl", "晴天 - 周杰倫"},
	}
	for _, tc := range cases {
		got := MatchScore(tc.query, tc.target)
		if got > maxScore {
			t.Errorf("MatchScore(%q, %q) = %.4f, 期望 <= %.2f", tc.query, tc.target, got, maxScore)
		}
		t.Logf("MatchScore(%q, %q) = %.4f", tc.query, tc.target, got)
	}
}

// 相关结果必须明显高于无关结果，否则排序无意义。
func TestMatchScoreOrdering(t *testing.T) {
	query := "lemon kenshi yonezu"
	relevant := MatchScore(query, "Lemon - Kenshi Yonezu")
	partial := MatchScore(query, "Lemon (Cover) - Someone Else")
	irrelevant := MatchScore(query, "Bohemian Rhapsody - Queen")

	t.Logf("相关=%.4f 部分=%.4f 无关=%.4f", relevant, partial, irrelevant)
	if !(relevant > partial && partial > irrelevant) {
		t.Errorf("排序错误：相关(%.4f) 应 > 部分(%.4f) 应 > 无关(%.4f)", relevant, partial, irrelevant)
	}
}

// 错字容忍：用户手打歌名难免打错一两个字母。
func TestMatchScoreTypoTolerance(t *testing.T) {
	cases := []struct {
		query string
		min   float64
	}{
		{"lemmon", 0.6}, // 多一个字母
		{"lemn", 0.55},  // 少一个字母
		{"lemom", 0.6},  // 错一个字母
	}
	for _, tc := range cases {
		got := MatchScore(tc.query, "Lemon")
		if got < tc.min {
			t.Errorf("MatchScore(%q, \"Lemon\") = %.4f, 期望 >= %.2f", tc.query, got, tc.min)
		}
		t.Logf("MatchScore(%q, \"Lemon\") = %.4f", tc.query, got)
	}
}

// 错拼词元落在完整 display_name 里也必须拿到高分。
//
// 这是词元级模糊匹配存在的理由：真实 target 是 "Lemon - Kenshi Yonezu"
// 而不是裸标题 "Lemon"，整串 Levenshtein 会被歌手部分严重稀释
// （实测 "lemmon" 只有 0.2632，和乱码 query 的 0.2059 无从区分）。
func TestMatchScoreTypoAgainstFullDisplayName(t *testing.T) {
	const target = "Lemon - Kenshi Yonezu"
	// 无关 query 的分值上限，错拼必须明显高于它。
	irrelevant := MatchScore("zzqqxxwweeyy nonexistent song 9182", target)

	cases := []struct {
		query string
		min   float64
	}{
		{"lemmon", 0.7},        // 歌名多一个字母
		{"kenshi yonzu", 0.7},  // 歌手少一个字母
		{"lemon yonezuu", 0.7}, // 歌名正确 + 歌手多一个字母
		{"kenshl yonezu", 0.7}, // i 误打成 l
	}
	for _, tc := range cases {
		t.Run(tc.query, func(t *testing.T) {
			got := MatchScore(tc.query, target)
			if got < tc.min {
				t.Errorf("MatchScore(%q, %q) = %.4f, 期望 >= %.2f", tc.query, target, got, tc.min)
			}
			if got <= irrelevant {
				t.Errorf("MatchScore(%q, %q) = %.4f, 必须高于无关 query 的 %.4f", tc.query, target, got, irrelevant)
			}
			t.Logf("MatchScore(%q, %q) = %.4f", tc.query, target, got)
		})
	}
}

// 词元级模糊匹配不得放宽到「不同的词」，否则乱码 query 也能骗到高分。
func TestMatchScoreFuzzyTokenDoesNotOvermatch(t *testing.T) {
	const maxScore = 0.6
	cases := []struct{ query, target string }{
		{"lemon tree", "Lemon - Kenshi Yonezu"}, // tree 不该模糊命中 kenshi/yonezu
		{"bohemian rhapsody", "Lemon - Kenshi Yonezu"},
		{"random words here", "Lemon - Kenshi Yonezu"},
	}
	for _, tc := range cases {
		got := MatchScore(tc.query, tc.target)
		if got > maxScore {
			t.Errorf("MatchScore(%q, %q) = %.4f, 期望 <= %.2f（模糊匹配过宽）", tc.query, tc.target, got, maxScore)
		}
		t.Logf("MatchScore(%q, %q) = %.4f", tc.query, tc.target, got)
	}
}

// 精确命中必须严格优于错拼命中，否则搜索结果排序会把错的排到对的前面。
func TestMatchScoreExactBeatsFuzzy(t *testing.T) {
	exact := MatchScore("lemon kenshi yonezu", "Lemon - Kenshi Yonezu")
	typo := MatchScore("lemmon kenshi yonezu", "Lemon - Kenshi Yonezu")
	t.Logf("精确=%.4f 错拼=%.4f", exact, typo)
	if exact <= typo {
		t.Errorf("精确命中(%.4f) 必须高于错拼命中(%.4f)", exact, typo)
	}
}

// 模糊匹配不得抢走精确命中所需的 target 词元（两轮匹配的存在理由）。
func TestMatchScoreFuzzyDoesNotStealExactToken(t *testing.T) {
	// target 只有一个 "yonezu"；query 里 "yonezu" 应精确命中，
	// "yonezuu" 不能先抢走它导致 "yonezu" 反而落空。
	withBoth := MatchScore("yonezu yonezuu", "Yonezu - Artist")
	onlyExact := MatchScore("yonezu", "Yonezu - Artist")
	t.Logf("精确+错拼=%.4f 仅精确=%.4f", withBoth, onlyExact)
	if withBoth <= 0 {
		t.Errorf("MatchScore = %.4f，精确词元被模糊匹配抢占了", withBoth)
	}
}

// 单个汉字不得参与模糊匹配（Levenshtein 对单字只有 0/1，会大量误命中）。
func TestMatchScoreCJKSingleCharNoFuzzy(t *testing.T) {
	// 「爱」与「晴天 周杰倫」里任何一个字都不同，词元层面应完全落空，
	// 分值只能来自整串相似度，必须很低。
	got := MatchScore("爱", "晴天 - 周杰倫")
	t.Logf("MatchScore(\"爱\", \"晴天 - 周杰倫\") = %.4f", got)
	if got > 0.35 {
		t.Errorf("MatchScore = %.4f，单字模糊匹配过宽", got)
	}
}

// 重复词元不应被同一个 target 词元反复命中。
func TestMatchScoreRepeatedTokens(t *testing.T) {
	full := MatchScore("la la la", "La La La - Naughty Boy")
	single := MatchScore("la la la", "La - Someone")
	t.Logf("完整重复=%.4f 单个=%.4f", full, single)
	if full <= single {
		t.Errorf("完整重复词元(%.4f) 应高于只有一个的(%.4f)", full, single)
	}
}

// similarityUpperBound 被用作模糊匹配的剪枝条件，它必须是真正的上界，
// 否则会剪掉本应命中的词元（剪枝就变成了行为改变）。
func TestSimilarityUpperBoundIsValid(t *testing.T) {
	words := []string{
		"", "a", "ab", "lemon", "lemmon", "lemn", "kenshi", "yonezu", "yonzu",
		"bohemian", "rhapsody", "queen", "zzqqxxwweeyy", "nonexistent",
		"averyverylongtokenhere", "晴天", "周杰倫",
	}
	for _, a := range words {
		for _, b := range words {
			ar, br := []rune(a), []rune(b)
			actual := similarity(ar, br)
			upper := similarityUpperBound(len(ar), len(br))
			if actual > upper+1e-9 {
				t.Errorf("similarity(%q,%q)=%.6f 超过上界 %.6f", a, b, actual, upper)
			}
		}
	}
}

// 超长输入必须被截断，否则 O(n·m) 的 Levenshtein 会成为 DoS 向量
// （/search 的 query 完全由 bot 用户控制）。
func TestMatchScoreLongInputIsBounded(t *testing.T) {
	long := strings.Repeat("a", 100_000) + " " + strings.Repeat("b", 100_000)

	start := time.Now()
	got := MatchScore(long, "Lemon - Kenshi Yonezu")
	elapsed := time.Since(start)
	t.Logf("超长 query 打分 = %.4f，耗时 %v", got, elapsed)
	if elapsed > 50*time.Millisecond {
		t.Errorf("超长 query 耗时 %v，超过 50ms（截断未生效）", elapsed)
	}
	if got < 0 || got > 1 {
		t.Errorf("分值 %v 超出 [0,1]", got)
	}

	start = time.Now()
	got = MatchScore(long, long+"c")
	elapsed = time.Since(start)
	t.Logf("双侧超长打分 = %.4f，耗时 %v", got, elapsed)
	if elapsed > 50*time.Millisecond {
		t.Errorf("双侧超长耗时 %v，超过 50ms（截断未生效）", elapsed)
	}

	// 截断不得破坏「归一化后完全相同 = 1.0」这条契约。
	if got := MatchScore(long, long); got != 1 {
		t.Errorf("超长且相同的输入 = %v，期望 1.0", got)
	}
}

// 截断边界：正常长度的歌名不受影响，多字节字符不被切坏。
func TestTruncateRunes(t *testing.T) {
	cases := []struct {
		in    string
		limit int
		want  string
	}{
		{"lemon", 10, "lemon"},
		{"lemon", 5, "lemon"},
		{"lemon", 3, "lem"},
		{"晴天周杰倫", 2, "晴天"},
		{"晴天周杰倫", 5, "晴天周杰倫"},
		{"", 5, ""},
		{"lemon", 0, ""},
	}
	for _, tc := range cases {
		if got := truncateRunes(tc.in, tc.limit); got != tc.want {
			t.Errorf("truncateRunes(%q, %d) = %q, 期望 %q", tc.in, tc.limit, got, tc.want)
		}
	}
}

// 简繁差异：只记录实际分值，作为 min_score 默认阈值的取值依据（plan.md 要求）。
func TestMatchScoreSimplifiedTraditional(t *testing.T) {
	cases := []struct{ query, target string }{
		{"周杰伦 晴天", "晴天 - 周杰倫"}, // 伦/倫
		{"晴天 周杰伦", "晴天 - 周杰倫"},
		{"晴天", "晴天 - 周杰倫"},     // 标题部分无简繁差异
		{"周杰伦", "晴天 - 周杰倫"},    // 只有歌手，且有简繁差异
		{"邓紫棋 泡沫", "泡沫 - 鄧紫棋"}, // 邓/鄧
	}
	for _, tc := range cases {
		got := MatchScore(tc.query, tc.target)
		t.Logf("简繁 MatchScore(%q, %q) = %.4f", tc.query, tc.target, got)
		if got <= 0 {
			t.Errorf("MatchScore(%q, %q) = %v，简繁差异不应导致 0 分", tc.query, tc.target, got)
		}
	}
}

// 打印一张分值表，便于给 bot 侧推荐 min_score 阈值。
func TestScoreTableForThreshold(t *testing.T) {
	target := "Lemon - Kenshi Yonezu"
	queries := []string{
		"Lemon - Kenshi Yonezu",
		"lemon kenshi yonezu",
		"kenshi yonezu lemon",
		"lemon",
		"kenshi yonezu",
		"lemmon",
		"kenshi yonzu",
		"yonezu",
		"lemon tree",
		"bohemian rhapsody",
		"zzqqxxwweeyy nonexistent song 9182",
	}
	for _, q := range queries {
		fmt.Printf("  %-40s -> %.4f\n", q, MatchScore(q, target))
	}
}

func BenchmarkMatchScoreLatin(b *testing.B) {
	for b.Loop() {
		MatchScore("lemon kenshi yonezu", "Lemon - Kenshi Yonezu")
	}
}

func BenchmarkMatchScoreCJK(b *testing.B) {
	for b.Loop() {
		MatchScore("周杰伦 晴天", "晴天 - 周杰倫")
	}
}

func BenchmarkMatchScoreWorstCase(b *testing.B) {
	query := "zzqqxxwweeyy nonexistent song title that is quite long 9182"
	target := "Some Very Long Song Title Here (Official Music Video) - Artist One, Artist Two"
	for b.Loop() {
		MatchScore(query, target)
	}
}

// BenchmarkScoreFullPage 模拟一次 /search 的真实开销：给一页 20 条结果打分。
func BenchmarkScoreFullPage(b *testing.B) {
	targets := make([]string, 20)
	for i := range targets {
		targets[i] = fmt.Sprintf("Song Title %d (Official Video) - Artist %d, Featured Artist", i, i)
	}
	for b.Loop() {
		for _, t := range targets {
			MatchScore("song title 7 artist", t)
		}
	}
}

func BenchmarkNormalize(b *testing.B) {
	for b.Loop() {
		Normalize("Lemon (Official Music Video) - Kenshi Yonezu")
	}
}

func BenchmarkTokenizeCJK(b *testing.B) {
	normalized := Normalize("晴天 周杰倫 钢琴版")
	for b.Loop() {
		Tokenize(normalized)
	}
}

func BenchmarkBuildDisplayName(b *testing.B) {
	artists := []string{"Kenshi Yonezu", "Featured Artist"}
	for b.Loop() {
		BuildDisplayName("Lemon", artists)
	}
}
