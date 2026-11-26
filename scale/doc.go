// Package scale maps data values to pixel positions and generates
// human-friendly axis ticks.
//
// It provides three scales:
//
//   - [Linear] for continuous numbers, with "nice" domain rounding and
//     1-2-5 tick steps.
//   - [Time] for time.Time values, with calendar-aware ticks
//     (nanoseconds through years) that respect a time.Location and its
//     daylight-saving changes.
//   - [Band] for categorical values such as bar chart categories.
//
// Formatting follows US conventions: numbers use comma thousands
// separators and a typographic minus sign ([Minus]), and time labels use
// a 12-hour clock ("3 PM") and month-first dates ("Mar 3"), with weeks
// starting on Sunday. Callers that need another locale pass their own
// formatters (hyperplot's Axis.TimeFormat and Axis.TimeValueFormat).
//
// The package has no dependencies outside the standard library and is
// usable on its own, independent of hyperplot's chart renderer.
package scale
