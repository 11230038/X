package config

import "fmt"

// DashboardConfig holds the one number the "continue studying" view has.
//
// It is one key and it gets a file of its own rather than a corner of
// MasteryConfig, because it is not about mastery: the dashboard is the one read
// that fans in over every domain (§2.12), and a key about it filed under one of
// the domains it reads would be misfiled in the way that lasts — somebody
// looking for it would look in the wrong file, and somebody tidying that file
// would find it and wonder.
type DashboardConfig struct {
	// RecentLimit is how many entries each category of the dashboard carries. It
	// bounds every category at once, which is what makes it one key and not six:
	// the categories differ in what they hold, not in how much of it a person
	// wants to see on one screen.
	RecentLimit int
}

// Validate checks the limit.
//
// A zero limit would be a dashboard that shows nothing while still doing all of
// its reads, which is a shape nobody wants and a shape a missing key produces.
// There is no maximum for the same reason the question count has no ceiling
// here: this is a read, and asking for more rows than a screen holds costs a
// query rather than a model call.
func (c DashboardConfig) Validate() error {
	if c.RecentLimit <= 0 {
		return fmt.Errorf("DASHBOARD_RECENT_LIMIT must be a positive integer")
	}
	return nil
}

func loadDashboardConfig(values map[string]string) (DashboardConfig, error) {
	limit, err := parsePositiveInt(values, "DASHBOARD_RECENT_LIMIT")
	if err != nil {
		return DashboardConfig{}, err
	}
	return DashboardConfig{RecentLimit: limit}, nil
}
