package log

import (
	"testing"
	"time"
)

// ─── toLogFilter 边界测试 ────────────────────────────────────────

// TestToLogFilter_SingleLevel 验证只取第一个匹配的 level（return f 语义）。
func TestToLogFilter_SingleLevel(t *testing.T) {
	sel := LogSelector{
		Levels: []string{"warn"},
	}
	f := sel.toLogFilter()
	if f.Level != LogLevelWarn {
		t.Errorf("expected level=warn, got %q", f.Level)
	}
	if f.HasError != nil {
		t.Error("expected HasError nil for warn level")
	}
}

// TestToLogFilter_MultipleLevelsOnlyFirst 验证多 level 只取第一个。
func TestToLogFilter_MultipleLevelsOnlyFirst(t *testing.T) {
	sel := LogSelector{
		Levels: []string{"info", "debug", "error"},
	}
	f := sel.toLogFilter()
	if f.Level != LogLevelInfo {
		t.Errorf("expected level=info (first match), got %q", f.Level)
	}
}

// TestToLogFilter_ErrorLevelSetsHasError 验证 error level 同时设置 HasError。
func TestToLogFilter_ErrorLevelSetsHasError(t *testing.T) {
	sel := LogSelector{
		Levels: []string{"error"},
	}
	f := sel.toLogFilter()
	if f.Level != LogLevelError {
		t.Errorf("expected level=error, got %q", f.Level)
	}
	if f.HasError == nil || !*f.HasError {
		t.Error("expected HasError=true for error level")
	}
}

// TestToLogFilter_ErrorFirstStopsAtError 验证 error 在首位，后续 level 被忽略。
func TestToLogFilter_ErrorFirstStopsAtError(t *testing.T) {
	sel := LogSelector{
		Levels: []string{"error", "debug"},
	}
	f := sel.toLogFilter()
	if f.Level != LogLevelError {
		t.Errorf("expected level=error (first match), got %q", f.Level)
	}
	if f.HasError == nil || !*f.HasError {
		t.Error("expected HasError=true for error level")
	}
}

// TestToLogFilter_EmptyLevels 验证空 Levels 不设置 level。
func TestToLogFilter_EmptyLevels(t *testing.T) {
	sel := LogSelector{}
	f := sel.toLogFilter()
	if f.Level != "" {
		t.Errorf("expected empty level, got %q", f.Level)
	}
	if f.HasError != nil {
		t.Error("expected HasError nil for no levels")
	}
}

// TestToLogFilter_UnknownLevel 验证未知 level 字符串被跳过。
func TestToLogFilter_UnknownLevel(t *testing.T) {
	sel := LogSelector{
		Levels: []string{"verbose"},
	}
	f := sel.toLogFilter()
	if f.Level != "" {
		t.Errorf("expected empty level for unknown, got %q", f.Level)
	}
}

// TestToLogFilter_UnknownThenValidLevel 验证未知 level 在前时跳过并取首个有效 level。
// 回归 W3：旧实现 return f 在循环内无条件下返回，导致 ["verbose","info"] 返回空 level。
func TestToLogFilter_UnknownThenValidLevel(t *testing.T) {
	sel := LogSelector{
		Levels: []string{"verbose", "info"},
	}
	f := sel.toLogFilter()
	if f.Level != LogLevelInfo {
		t.Errorf("expected level %q (skip unknown verbose), got %q", LogLevelInfo, f.Level)
	}
}

// TestToLogFilter_TrackType 验证 TrackType 传递到 LogFilter。
func TestToLogFilter_TrackType(t *testing.T) {
	sel := LogSelector{
		TrackType: TrackRuns,
	}
	f := sel.toLogFilter()
	if f.TrackType != TrackRuns {
		t.Errorf("expected TrackType=%q, got %q", TrackRuns, f.TrackType)
	}
}

// TestToLogFilter_TrackTypeEmpty 验证空 TrackType 保持默认。
func TestToLogFilter_TrackTypeEmpty(t *testing.T) {
	sel := LogSelector{}
	f := sel.toLogFilter()
	if f.TrackType != "" {
		t.Errorf("expected empty TrackType, got %q", f.TrackType)
	}
}

// TestToLogFilter_TypesCategories 验证 Types→Categories 映射包含专用类别。
func TestToLogFilter_TypesCategories(t *testing.T) {
	tests := []struct {
		types []string
		wantCats []LogCategory
	}{
		{
			types: []string{"turn"},
			wantCats: []LogCategory{LogCategoryTurn, LogCategoryAgent},
		},
		{
			types: []string{"item"},
			wantCats: []LogCategory{LogCategoryItem, LogCategoryTool, LogCategoryLLM, LogCategoryCompact, LogCategoryHITL},
		},
		{
			types: []string{"event"},
			wantCats: []LogCategory{LogCategoryEvent, LogCategorySystem},
		},
		{
			types: []string{"session"},
			wantCats: []LogCategory{LogCategorySession},
		},
	}
	for _, tt := range tests {
		t.Run(tt.types[0], func(t *testing.T) {
			sel := LogSelector{Types: tt.types}
			f := sel.toLogFilter()
			if len(f.Categories) != len(tt.wantCats) {
				t.Fatalf("expected %d categories, got %d: %v", len(tt.wantCats), len(f.Categories), f.Categories)
			}
			for i, want := range tt.wantCats {
				if f.Categories[i] != want {
					t.Errorf("category[%d]: expected %q, got %q", i, want, f.Categories[i])
				}
			}
		})
	}
}

// TestToLogFilter_TimeRange 验证 Since/Until 传递。
func TestToLogFilter_TimeRange(t *testing.T) {
	now := time.Now().UTC()
	later := now.Add(time.Hour)
	sel := LogSelector{
		Since: &now,
		Until: &later,
	}
	f := sel.toLogFilter()
	if f.StartTime == nil || !f.StartTime.Equal(now) {
		t.Error("StartTime not propagated")
	}
	if f.EndTime == nil || !f.EndTime.Equal(later) {
		t.Error("EndTime not propagated")
	}
}

// TestToLogFilter_Limit 验证 Limit 传递。
func TestToLogFilter_Limit(t *testing.T) {
	sel := LogSelector{Limit: 42}
	f := sel.toLogFilter()
	if f.Limit != 42 {
		t.Errorf("expected Limit=42, got %d", f.Limit)
	}
}

// TestToLogFilter_Tags 验证 Tags 传递。
func TestToLogFilter_Tags(t *testing.T) {
	sel := LogSelector{Tags: []string{"tool:edit", "provider:openai"}}
	f := sel.toLogFilter()
	if len(f.Tags) != 2 || f.Tags[0] != "tool:edit" || f.Tags[1] != "provider:openai" {
		t.Errorf("Tags not propagated: %v", f.Tags)
	}
}
