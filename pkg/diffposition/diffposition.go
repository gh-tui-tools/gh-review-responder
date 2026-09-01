package diffposition

import (
	"github.com/gh-tui-tools/gh-review-responder/pkg/diffhunk"
)

// MapOldPositionToNew maps a line number from the old file to the new file
// using the diff patch. Returns the new line number (1-based) or -1 if not found.
func MapOldPositionToNew(patch string, oldLine int) (int, error) {
	hunks, err := diffhunk.ParsePatch(patch)
	if err != nil {
		return -1, err
	}

	// If line is before any hunks, it's unchanged
	if len(hunks) > 0 && oldLine < hunks[0].OldStart {
		return oldLine, nil
	}

	offset := 0 // Track cumulative offset from additions/deletions

	for _, hunk := range hunks {
		// If line is before this hunk, it's unchanged (but apply previous offset)
		if oldLine < hunk.OldStart {
			return oldLine + offset, nil
		}

		// If line is after this hunk, update offset and continue
		if oldLine >= hunk.OldStart+hunk.OldLines {
			// This hunk affects the offset for lines after it
			offset += (hunk.NewLines - hunk.OldLines)
			continue
		}

		// Line is within this hunk - need to find exact mapping
		currentOldLine := hunk.OldStart
		currentNewLine := hunk.NewStart

		for _, line := range hunk.Lines {
			switch line.Type {
			case diffhunk.Context:
				if currentOldLine == oldLine {
					return currentNewLine, nil
				}
				currentOldLine++
				currentNewLine++
			case diffhunk.Delete:
				if currentOldLine == oldLine {
					// This line was deleted, no corresponding new line
					return -1, nil
				}
				currentOldLine++
			case diffhunk.Add:
				currentNewLine++
			}
		}
	}

	// Line is after all hunks
	return oldLine + offset, nil
}

// MapNewPositionToOld maps a line number from the new file to the old file
// using the diff patch. Returns the old line number (1-based) or -1 if not found.
func MapNewPositionToOld(patch string, newLine int) (int, error) {
	hunks, err := diffhunk.ParsePatch(patch)
	if err != nil {
		return -1, err
	}

	// If line is before any hunks, it's unchanged
	if len(hunks) > 0 && newLine < hunks[0].NewStart {
		return newLine, nil
	}

	offset := 0 // Track cumulative offset from additions/deletions

	for _, hunk := range hunks {
		// If line is before this hunk, it's unchanged (but apply previous offset)
		if newLine < hunk.NewStart {
			return newLine - offset, nil
		}

		// If line is after this hunk, update offset and continue
		if newLine >= hunk.NewStart+hunk.NewLines {
			// This hunk affects the offset for lines after it
			offset += (hunk.NewLines - hunk.OldLines)
			continue
		}

		// Line is within this hunk - need to find exact mapping
		currentOldLine := hunk.OldStart
		currentNewLine := hunk.NewStart

		for _, line := range hunk.Lines {
			switch line.Type {
			case diffhunk.Context:
				if currentNewLine == newLine {
					return currentOldLine, nil
				}
				currentOldLine++
				currentNewLine++
			case diffhunk.Delete:
				currentOldLine++
			case diffhunk.Add:
				if currentNewLine == newLine {
					// This line was added, no corresponding old line
					return -1, nil
				}
				currentNewLine++
			}
		}
	}

	// Line is after all hunks
	return newLine - offset, nil
}

// DiffSide represents which side of a diff a comment is on
type DiffSide string

const (
	DiffSideLeft  DiffSide = "LEFT"  // Original/base file
	DiffSideRight DiffSide = "RIGHT" // Modified/new file
)

// CommentPosition represents the position of a comment in a diff
type CommentPosition struct {
	Line              int      // Current line number (1-based)
	OriginalLine      int      // Original line number (1-based)
	StartLine         int      // For multi-line comments
	EndLine           int      // For multi-line comments
	OriginalStartLine int      // For multi-line comments
	OriginalEndLine   int      // For multi-line comments
	DiffSide          DiffSide // Which side of the diff
	IsOutdated        bool     // Whether the comment is on outdated code
}

// CalculateCommentPosition calculates the comment position from GitHub API data
func CalculateCommentPosition(line, originalLine int, diffHunk string, diffSide DiffSide) (*CommentPosition, error) {
	pos := &CommentPosition{
		Line:              line,
		OriginalLine:      originalLine,
		StartLine:         line,
		EndLine:           line,
		OriginalStartLine: originalLine,
		OriginalEndLine:   originalLine,
		DiffSide:          diffSide,
	}

	// Check if position is outdated by trying to map between old and new
	if diffSide == DiffSideRight && diffHunk != "" {
		// For right side, check if we can map new line to old line
		oldLine, err := MapNewPositionToOld(diffHunk, line)
		if err == nil && oldLine == -1 {
			// Line was added, so it's not outdated
			pos.IsOutdated = false
		} else if err == nil && oldLine != originalLine {
			// Mapping exists but doesn't match original - potentially outdated
			pos.IsOutdated = true
		}
	} else if diffSide == DiffSideLeft && diffHunk != "" {
		// For left side, check if we can map old line to new line
		newLine, err := MapOldPositionToNew(diffHunk, originalLine)
		if err == nil && newLine == -1 {
			// Line was deleted, mark as outdated
			pos.IsOutdated = true
		}
	}

	return pos, nil
}

// GetCommentingRanges determines which line ranges are valid for commenting
// For base file (left): only deleted lines are commentable
// For modified file (right): all changed lines are commentable
func GetCommentingRanges(patch string, isBase bool) ([][2]int, error) {
	hunks, err := diffhunk.ParsePatch(patch)
	if err != nil {
		return nil, err
	}

	var ranges [][2]int

	for _, hunk := range hunks {
		var rangeStart, rangeEnd int
		inRange := false

		for _, line := range hunk.Lines {
			var lineNum int
			var isCommentable bool

			if isBase {
				// For base file, only deleted and context lines are commentable
				lineNum = line.OldLineNumber
				isCommentable = line.Type == diffhunk.Delete || line.Type == diffhunk.Context
			} else {
				// For modified file, added and context lines are commentable
				lineNum = line.NewLineNumber
				isCommentable = line.Type == diffhunk.Add || line.Type == diffhunk.Context
			}

			if isCommentable && lineNum > 0 {
				if !inRange {
					rangeStart = lineNum
					rangeEnd = lineNum
					inRange = true
				} else {
					rangeEnd = lineNum
				}
			} else if inRange {
				// End of range
				ranges = append(ranges, [2]int{rangeStart, rangeEnd})
				inRange = false
			}
		}

		// Close last range if still open
		if inRange {
			ranges = append(ranges, [2]int{rangeStart, rangeEnd})
		}
	}

	return ranges, nil
}
