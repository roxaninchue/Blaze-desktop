package connect

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Google Classroom (v0.15, student pack): the student's courses and coursework, read
// only, through the existing Google connector. The scopes are asked for on first use
// (OnDemand), so connecting Google for Gmail never asks for Classroom.
var classroomAPI = "https://classroom.googleapis.com/v1"

const (
	scopeCourses    = gScope + "classroom.courses.readonly"
	scopeCoursework = gScope + "classroom.coursework.me.readonly"
)

type gCourse struct {
	ID, Name, Section, Room, AlternateLink, CourseState string
}

type gDate struct{ Year, Month, Day int }
type gTime struct{ Hours, Minutes int }

type gWork struct {
	ID, CourseID, Title, Description, State, WorkType, AlternateLink string
	MaxPoints                                                        float64
	DueDate                                                          *gDate
	DueTime                                                          *gTime
}

// due is the coursework's due moment (Classroom stores it in UTC), or zero.
func (w gWork) due() time.Time {
	if w.DueDate == nil || w.DueDate.Year == 0 {
		return time.Time{}
	}
	h, m := 23, 59
	if w.DueTime != nil {
		h, m = w.DueTime.Hours, w.DueTime.Minutes
	}
	return time.Date(w.DueDate.Year, time.Month(w.DueDate.Month), w.DueDate.Day, h, m, 0, 0, time.UTC)
}

func (c *call) courses() ([]gCourse, error) {
	var r struct {
		Courses []gCourse `json:"courses"`
	}
	if err := c.get(classroomAPI+"/courses?studentId=me&courseStates=ACTIVE&pageSize=50", &r); err != nil {
		return nil, err
	}
	return r.Courses, nil
}

func classroomTools() []*Tool {
	return []*Tool{
		{Name: "classroom_courses", Group: "classroom", Risk: Read, Scopes: []string{scopeCourses}, OnDemand: true,
			Desc: "List the user's active Google Classroom courses (as a student): id, name, section, room and link. Ids feed classroom_coursework.",
			Run: func(c *call) (string, error) {
				cs, err := c.courses()
				if err != nil {
					return "", err
				}
				if len(cs) == 0 {
					return "No active Google Classroom courses on this account.", nil
				}
				var b strings.Builder
				for _, x := range cs {
					fmt.Fprintf(&b, "%s", x.Name)
					if x.Section != "" {
						b.WriteString(" · " + x.Section)
					}
					if x.Room != "" {
						b.WriteString(" · room " + x.Room)
					}
					fmt.Fprintf(&b, " [%s] %s\n", x.ID, x.AlternateLink)
				}
				return b.String(), nil
			}},
		{Name: "classroom_coursework", Group: "classroom", Risk: Read, Scopes: []string{scopeCourses, scopeCoursework}, OnDemand: true,
			Desc:  "List Google Classroom assignments and quizzes with due dates (local time) and whether the user turned them in. Default: every active course, due from today on. For classroom-sync: each upcoming item becomes a Schedule reminder 24h before.",
			Props: map[string]any{"course_id": str("Optional course id from classroom_courses (empty = every active course)"), "days": num("Due within this many days (default 30, up to 180)"), "include_past": map[string]any{"type": "boolean", "description": "Also list work whose due date passed (default false)"}},
			Run: func(c *call) (string, error) {
				var cs []gCourse
				if id := c.a.s("course_id"); id != "" {
					cs = []gCourse{{ID: id, Name: id}}
				} else {
					var err error
					if cs, err = c.courses(); err != nil {
						return "", err
					}
				}
				now := time.Now()
				until := now.AddDate(0, 0, c.a.i("days", 30, 1, 180))
				past := c.a.s("include_past") == "true"
				type row struct {
					when time.Time
					line string
				}
				var rows []row
				for _, x := range cs {
					var r struct {
						CourseWork []gWork `json:"courseWork"`
					}
					if err := c.get(classroomAPI+"/courses/"+q(x.ID)+"/courseWork?courseWorkStates=PUBLISHED&pageSize=60&orderBy=dueDate%20desc", &r); err != nil {
						return "", err
					}
					var subs struct {
						StudentSubmissions []struct {
							CourseWorkID, State string
							Late                bool
						} `json:"studentSubmissions"`
					}
					_ = c.get(classroomAPI+"/courses/"+q(x.ID)+"/courseWork/-/studentSubmissions?userId=me&pageSize=100", &subs)
					state := map[string]string{}
					for _, s := range subs.StudentSubmissions {
						st := strings.ToLower(strings.ReplaceAll(s.State, "_", " "))
						if s.Late {
							st += ", late"
						}
						state[s.CourseWorkID] = st
					}
					for _, w := range r.CourseWork {
						d := w.due()
						if d.IsZero() || d.After(until) || (!past && d.Before(now)) {
							continue
						}
						l := fmt.Sprintf("%s · %s · %s", d.Local().Format("Mon 2 Jan 15:04"), x.Name, w.Title)
						if w.MaxPoints > 0 {
							l += fmt.Sprintf(" · %g pts", w.MaxPoints)
						}
						if st := state[w.ID]; st != "" {
							l += " · " + st
						}
						l += fmt.Sprintf(" [course %s, work %s] %s", x.ID, w.ID, w.AlternateLink)
						rows = append(rows, row{d, l})
					}
				}
				if len(rows) == 0 {
					return "No Classroom work due in that window.", nil
				}
				sort.Slice(rows, func(i, j int) bool { return rows[i].when.Before(rows[j].when) })
				var b strings.Builder
				for _, r := range rows {
					b.WriteString(r.line + "\n")
				}
				return b.String(), nil
			}},
	}
}
