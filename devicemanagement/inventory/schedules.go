package inventory

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
)

// Schedule uses standard five-field cron and an IANA zone. Missed runs coalesce.
type Schedule struct {
	AccountID   string     `json:"account_id"`
	Expression  string     `json:"expression,omitempty"`
	RepeatEvery int        `json:"repeat_every,omitempty"`
	RepeatUnit  string     `json:"repeat_unit,omitempty"`
	Anchor      time.Time  `json:"anchor,omitempty"`
	TimeZone    string     `json:"time_zone"`
	Enabled     bool       `json:"enabled"`
	Next        time.Time  `json:"next"`
	Last        *time.Time `json:"last,omitempty"`
}

// NextRun computes the next matching minute, including DST transitions in the zone.
func NextRun(expression, zone string, after time.Time) (time.Time, error) {
	if zone == "" {
		zone = "UTC"
	}
	loc, e := time.LoadLocation(zone)
	if e != nil {
		return time.Time{}, ErrInvalid
	}
	parts := strings.Fields(expression)
	if len(parts) != 5 {
		return time.Time{}, ErrInvalid
	}
	mins := []int{0, 0, 1, 1, 0}
	maxs := []int{59, 23, 31, 12, 6}
	sets := make([]map[int]bool, 5)
	for i, part := range parts {
		set, e := cronSet(part, mins[i], maxs[i])
		if e != nil {
			return time.Time{}, e
		}
		sets[i] = set
	}
	start := after.Truncate(time.Minute).Add(time.Minute)
	end := start.AddDate(5, 0, 0)
	for t := start; t.Before(end); t = t.Add(time.Minute) {
		local := t.In(loc)
		day := sets[2][local.Day()]
		week := sets[4][int(local.Weekday())]
		calendar := day && week
		if parts[2] != "*" && parts[4] != "*" {
			calendar = day || week
		}
		if sets[0][local.Minute()] && sets[1][local.Hour()] && calendar && sets[3][int(local.Month())] {
			return t.UTC(), nil
		}
	}
	return time.Time{}, ErrInvalid
}

// cronSet expands bounded cron ranges, lists and steps.
func cronSet(s string, min, max int) (map[int]bool, error) {
	out := map[int]bool{}
	for _, item := range strings.Split(s, ",") {
		step := 1
		sp := strings.Split(item, "/")
		if len(sp) > 2 {
			return nil, ErrInvalid
		}
		if len(sp) == 2 {
			n, e := strconv.Atoi(sp[1])
			if e != nil || n < 1 || n > max+1 {
				return nil, ErrInvalid
			}
			step = n
		}
		lo, hi := min, max
		if sp[0] != "*" {
			bounds := strings.Split(sp[0], "-")
			var e error
			lo, e = strconv.Atoi(bounds[0])
			if e != nil {
				return nil, ErrInvalid
			}
			hi = lo
			if len(bounds) == 2 {
				hi, e = strconv.Atoi(bounds[1])
				if e != nil {
					return nil, ErrInvalid
				}
			} else if len(bounds) > 2 {
				return nil, ErrInvalid
			} else if len(sp) == 2 {
				hi = max
			}
		}
		if lo < min || hi > max || lo > hi {
			return nil, ErrInvalid
		}
		for n := lo; n <= hi; n += step {
			out[n] = true
		}
	}
	return out, nil
}

// SaveSchedule validates syntax before replacing an account's schedule.
func (r *Repository) SaveSchedule(ctx context.Context, s Schedule, now time.Time) (Schedule, error) {
	if s.TimeZone == "" {
		s.TimeZone = "UTC"
	}
	if s.Anchor.IsZero() {
		s.Anchor = now
	}
	next, e := s.NextOccurrence(now)
	if e != nil {
		return s, e
	}
	s.Next = next
	e = r.Backend.Update(ctx, func(tx Tx) error {
		if _, e := get[Account](ctx, tx, "account/"+s.AccountID); e != nil {
			return e
		}
		return put(ctx, tx, "schedule/"+s.AccountID, s)
	})
	return s, e
}

// Schedules lists named account schedules.
func (r *Repository) Schedules(ctx context.Context) ([]Schedule, error) {
	out := []Schedule{}
	e := walk(ctx, r.Backend, "schedule/", func(entry Entry) error {
		var s Schedule
		if e := json.Unmarshal(entry.Value, &s); e != nil {
			return e
		}
		out = append(out, s)
		return nil
	})
	return out, e
}

// Tick coalesces due runs and advances only after enqueue succeeds.
func (r *Repository) Tick(ctx context.Context, now time.Time) error {
	schedules, e := r.Schedules(ctx)
	if e != nil {
		return e
	}
	for _, s := range schedules {
		if !s.Enabled || s.Next.After(now) {
			continue
		}
		if _, e := r.Enqueue(ctx, s.AccountID, false, now); e != nil {
			if errors.Is(e, ErrInvalid) || errors.Is(e, ErrNotFound) {
				continue
			}
			return e
		}
		next, e := s.NextOccurrence(now)
		if e != nil {
			return e
		}
		e = r.Backend.Update(ctx, func(tx Tx) error {
			current, e := get[Schedule](ctx, tx, "schedule/"+s.AccountID)
			if e != nil {
				return e
			}
			if !current.Next.Equal(s.Next) {
				return nil
			}
			current.Last = &now
			current.Next = next
			return put(ctx, tx, "schedule/"+s.AccountID, current)
		})
		if e != nil {
			return e
		}
	}
	return nil
}

// NextOccurrence supports the simple repeat builder as well as raw cron.
// Calendar days, weeks and months preserve the anchor's local wall time across DST;
// a short month uses its last day rather than spilling into the following month.
func (s Schedule) NextOccurrence(after time.Time) (time.Time, error) {
	if s.RepeatUnit == "" {
		if s.RepeatEvery != 0 {
			return time.Time{}, ErrInvalid
		}
		return NextRun(s.Expression, s.TimeZone, after)
	}
	if s.Expression != "" || s.RepeatEvery < 1 || s.RepeatEvery > 1000 || s.Anchor.IsZero() {
		return time.Time{}, ErrInvalid
	}
	zone := s.TimeZone
	if zone == "" {
		zone = "UTC"
	}
	loc, e := time.LoadLocation(zone)
	if e != nil {
		return time.Time{}, ErrInvalid
	}
	anchor := s.Anchor.In(loc)
	after = after.In(loc)
	step := s.RepeatEvery
	if s.RepeatUnit == "minutes" || s.RepeatUnit == "hours" {
		unit := time.Minute
		if s.RepeatUnit == "hours" {
			unit = time.Hour
		}
		interval := time.Duration(step) * unit
		count := int64(after.Sub(anchor)/interval) + 1
		if count < 1 {
			count = 1
		}
		return anchor.Add(time.Duration(count) * interval).UTC(), nil
	}
	var count int
	var candidate func(int) time.Time
	switch s.RepeatUnit {
	case "days", "weeks":
		days := step
		if s.RepeatUnit == "weeks" {
			days *= 7
		}
		count = int(after.Sub(anchor).Hours()/24) / days
		candidate = func(n int) time.Time { return anchor.AddDate(0, 0, n*days) }
	case "months":
		count = ((after.Year()-anchor.Year())*12 + int(after.Month()-anchor.Month())) / step
		candidate = func(n int) time.Time {
			first := time.Date(anchor.Year(), anchor.Month()+time.Month(n*step), 1, anchor.Hour(), anchor.Minute(), anchor.Second(), anchor.Nanosecond(), loc)
			last := first.AddDate(0, 1, -1).Day()
			day := anchor.Day()
			if day > last {
				day = last
			}
			return first.AddDate(0, 0, day-1)
		}
	default:
		return time.Time{}, ErrInvalid
	}
	if count < 1 {
		count = 1
	}
	for range 4 {
		next := candidate(count)
		if next.After(after) {
			if next.Year() > 9999 {
				return time.Time{}, ErrInvalid
			}
			return next.UTC(), nil
		}
		count++
	}
	return time.Time{}, ErrInvalid
}
