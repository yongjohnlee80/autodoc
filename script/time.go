package script

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	lua "github.com/yuin/gopher-lua"
)

// now and date stand in for os.time and os.date, which a script does not have (no os): the time,
// and the time written as a script asks.

// timeFunctions are the global table's now and date.
func (e *Engine) timeFunctions() []function {
	return []function{
		{"now", "", "number", "The time, in seconds since 1970-01-01 UTC, with a fraction: `os.time()`'s, to the millisecond.", e.luaNow},
		{"date", "format, t", "string, or a table", "The time t (seconds, `now()` by default) written by format, as `os.date` does: `%Y-%m-%d`, `%H:%M`, …; `%c` by default. A format starting with `!` is in UTC, else local time; `\"*t\"` gives a table: `year`, `month`, `day`, `hour`, `min`, `sec`, `wday`, `yday`, `isdst`.", e.luaDate},
	}
}

func (e *Engine) luaNow(L *lua.LState) int {
	t := e.clock()
	L.Push(lua.LNumber(float64(t.UnixMilli()) / 1000))
	return 1
}

func (e *Engine) luaDate(L *lua.LState) int {
	format := L.OptString(1, "%c")
	t := e.clock()
	if L.Get(2) != lua.LNil {
		s := float64(L.CheckNumber(2))
		t = time.UnixMilli(int64(s * 1000))
	}
	if rest, ok := strings.CutPrefix(format, "!"); ok {
		format, t = rest, t.UTC()
	} else {
		t = t.In(e.location)
	}
	if format == "*t" {
		tb := L.CreateTable(0, 9)
		for k, v := range map[string]int{"year": t.Year(), "month": int(t.Month()), "day": t.Day(), "hour": t.Hour(),
			"min": t.Minute(), "sec": t.Second(), "wday": int(t.Weekday()) + 1, "yday": t.YearDay()} {
			tb.RawSetString(k, lua.LNumber(v))
		}
		tb.RawSetString("isdst", lua.LBool(t.IsDST()))
		L.Push(tb)
		return 1
	}
	s, err := strftime(t, format)
	if err != nil {
		L.ArgError(1, err.Error())
	}
	L.Push(lua.LString(s))
	return 1
}

// strftime writes t by format's C conversions, the ones os.date takes on every platform.
func strftime(t time.Time, format string) (string, error) {
	var b strings.Builder
	two := func(n int) string { return fmt.Sprintf("%02d", n) }
	for i := 0; i < len(format); i++ {
		if format[i] != '%' {
			b.WriteByte(format[i])
			continue
		}
		if i++; i == len(format) {
			return "", fmt.Errorf("a format ending in %%")
		}
		switch format[i] {
		case 'Y':
			b.WriteString(strconv.Itoa(t.Year()))
		case 'y':
			b.WriteString(two(t.Year() % 100))
		case 'm':
			b.WriteString(two(int(t.Month())))
		case 'd':
			b.WriteString(two(t.Day()))
		case 'e':
			fmt.Fprintf(&b, "%2d", t.Day())
		case 'H':
			b.WriteString(two(t.Hour()))
		case 'I':
			b.WriteString(two((t.Hour()+11)%12 + 1))
		case 'M':
			b.WriteString(two(t.Minute()))
		case 'S':
			b.WriteString(two(t.Second()))
		case 'p':
			b.WriteString(t.Format("PM"))
		case 'a':
			b.WriteString(t.Format("Mon"))
		case 'A':
			b.WriteString(t.Format("Monday"))
		case 'b', 'h':
			b.WriteString(t.Format("Jan"))
		case 'B':
			b.WriteString(t.Format("January"))
		case 'j':
			fmt.Fprintf(&b, "%03d", t.YearDay())
		case 'w':
			b.WriteString(strconv.Itoa(int(t.Weekday())))
		case 'u':
			b.WriteString(strconv.Itoa((int(t.Weekday())+6)%7 + 1))
		case 'Z':
			b.WriteString(t.Format("MST"))
		case 'z':
			b.WriteString(t.Format("-0700"))
		case 'F':
			b.WriteString(t.Format("2006-01-02"))
		case 'T', 'X':
			b.WriteString(t.Format("15:04:05"))
		case 'R':
			b.WriteString(t.Format("15:04"))
		case 'D', 'x':
			b.WriteString(t.Format("01/02/06"))
		case 'c':
			b.WriteString(t.Format("Mon Jan  2 15:04:05 2006"))
		case 's':
			b.WriteString(strconv.FormatInt(t.Unix(), 10))
		case 'n':
			b.WriteByte('\n')
		case 't':
			b.WriteByte('\t')
		case '%':
			b.WriteByte('%')
		default:
			return "", fmt.Errorf("%%%c is not a conversion date knows", format[i])
		}
	}
	return b.String(), nil
}
