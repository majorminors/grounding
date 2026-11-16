package backends

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// The "auto" renderer climbs to Firecrawl without being asked, so a
// daily cap bounds what grounding can spend there. The count is kept in
// a file, not in memory, because every research_question run starts
// child grounding processes of its own: a per-process count would let
// each of them spend the cap again. The file holds one line, the UTC
// date and the credits spent on it, and is read and written under an
// exclusive flock.
//
// A credit is reserved before each scrape and the PDF pages Firecrawl
// says it parsed are added after, so scrapes in flight at once can go
// past the cap by at most one PDF's pages each. A scrape that fails
// keeps its reservation: whether Firecrawl billed it cannot be told
// from here, and erring that way errs cheap.

// ErrFirecrawlDailyCreditCapSpent is returned when today's cap is spent.
var ErrFirecrawlDailyCreditCapSpent = errors.New("firecrawl's daily credit cap is spent")

type firecrawlDailyCreditCap struct {
	credits    int    // the cap; 0 means none
	ledgerPath string // the file that counts today's spend
	now        func() time.Time
}

// WithDailyCreditCap caps the credits this backend spends in a UTC day,
// counted in the file at ledgerPath, which every grounding process on
// the host shares. credits 0 means no cap.
func (f *FirecrawlBackend) WithDailyCreditCap(credits int, ledgerPath string) *FirecrawlBackend {
	f.dailyCreditCap = &firecrawlDailyCreditCap{credits: credits, ledgerPath: ledgerPath, now: time.Now}
	return f
}

// reserve counts credits against today, or refuses when the cap would
// be passed. It is a no-op with no cap.
func (c *firecrawlDailyCreditCap) reserve(credits int) error {
	return c.update(func(spent int) (int, error) {
		if spent+credits > c.credits {
			return spent, fmt.Errorf("%w: %d of %d credits spent today (UTC); it resets at midnight UTC, and FIRECRAWL_DAILY_CREDIT_CAP sets it",
				ErrFirecrawlDailyCreditCapSpent, spent, c.credits)
		}
		return spent + credits, nil
	})
}

// add counts credits already spent, whatever the cap.
func (c *firecrawlDailyCreditCap) add(credits int) error {
	if credits <= 0 {
		return nil
	}
	return c.update(func(spent int) (int, error) { return spent + credits, nil })
}

// spentToday reads today's count.
func (c *firecrawlDailyCreditCap) spentToday() (int, error) {
	var today int
	err := c.update(func(spent int) (int, error) { today = spent; return spent, nil })
	return today, err
}

// update reads today's count, applies change and writes the result,
// holding the file's lock throughout. A ledger that cannot be opened
// refuses the scrape: an uncounted Firecrawl is an uncapped one.
func (c *firecrawlDailyCreditCap) update(change func(spent int) (int, error)) error {
	if c == nil || c.credits <= 0 {
		return nil
	}
	if c.ledgerPath == "" {
		return errors.New("firecrawl: no state directory to count the daily credit cap in, so it is not used")
	}
	if err := os.MkdirAll(filepath.Dir(c.ledgerPath), 0o700); err != nil {
		return fmt.Errorf("firecrawl: cannot keep the daily credit count, so it is not used: %w", err)
	}
	file, err := os.OpenFile(c.ledgerPath, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return fmt.Errorf("firecrawl: cannot keep the daily credit count, so it is not used: %w", err)
	}
	defer file.Close()
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("firecrawl: cannot lock the daily credit count: %w", err)
	}
	defer syscall.Flock(int(file.Fd()), syscall.LOCK_UN)

	today := c.now().UTC().Format(time.DateOnly)
	spent := 0
	contents, err := os.ReadFile(c.ledgerPath)
	if err != nil {
		return err
	}
	if date, count, found := strings.Cut(strings.TrimSpace(string(contents)), " "); found && date == today {
		spent, _ = strconv.Atoi(count)
	}
	updated, changeErr := change(spent)
	if updated != spent {
		if err := file.Truncate(0); err != nil {
			return err
		}
		if _, err := file.WriteAt([]byte(fmt.Sprintf("%s %d\n", today, updated)), 0); err != nil {
			return err
		}
	}
	return changeErr
}
