package tools

import (
	"regexp"
	"strings"
	"unicode"
)

// A site can answer a browser with nothing but its cookie or consent
// notice: the page's own content never loads until a choice is made.
// The renderer then reads the notice as the page, and it used to count
// as read: on 2026-11-08 three of four forum threads a researcher asked
// for came back as their title and the notice, nothing else.
//
// The notice is recognised by what it is, not by which site served it:
// the page is short, it carries a paragraph about cookies or consent
// that asks for a choice (accept, reject, manage...), and once every
// such paragraph, the title and the links are set aside, almost no text
// is left. A page whose content loaded beneath its banner keeps that
// content and is read as usual; a page about cookies that asks for no
// choice is read as usual too.

// consentWallDetectionMaximumCharacters bounds the pages looked at: a
// notice alone is short (the ones seen were 591 to 1,385 characters,
// the longest the notice twice over), and a long page has content of
// its own whatever its banner says.
const consentWallDetectionMaximumCharacters = 4000

// consentNoticeVocabulary marks a paragraph as part of a cookie or
// consent notice. "cookie" is the same word in most European languages.
// Consent alone is not enough: "informed consent" is in every clinical
// abstract.
var consentNoticeVocabulary = []string{"cookie", "we value your privacy", "your privacy choices", "manage consent"}

// consentNoticeChoicePhrases are the buttons a notice asks the reader
// to press; a paragraph that explains cookies without asking is not a
// wall.
var consentNoticeChoicePhrases = []string{
	"accept all", "accept cookies", "accept optional", "i accept", "i agree",
	"reject all", "reject optional", "reject non-essential", "reject cookies",
	"allow all", "allow cookies", "deny all", "decline",
	"manage cookies", "manage preferences", "manage options", "manage consent",
	"cookie preferences", "cookie settings", "privacy settings",
	"customise", "customize", "opt out",
}

var (
	markdownListItemLine = regexp.MustCompile(`^\s*([-*+]|\d+[.)])\s`)
	markdownBlankLine    = regexp.MustCompile(`\n\s*\n`)
)

// webPageIsOnlyACookieOrConsentNotice reports a page whose text,
// cookie or consent notice aside, is almost nothing.
func webPageIsOnlyACookieOrConsentNotice(markdown, title string) bool {
	if strings.TrimSpace(markdown) == "" || len([]rune(markdown)) > consentWallDetectionMaximumCharacters {
		return false
	}
	normalisedTitle := normalisedForConsentWallMatching(title)
	// A paragraph about cookies, or one offering the notice's buttons,
	// is part of the notice; the page must have both.
	foundCookieOrConsentWording, foundAChoiceToMake := false, false
	charactersOutsideTheNotice := 0
	for _, block := range markdownBlocksWithListsJoinedToTheirIntroduction(markdown) {
		searchable := normalisedForConsentWallMatching(block)
		aboutCookiesOrConsent := containsAnyOf(searchable, consentNoticeVocabulary)
		offersAChoice := containsAnyOf(searchable, consentNoticeChoicePhrases)
		foundCookieOrConsentWording = foundCookieOrConsentWording || aboutCookiesOrConsent
		foundAChoiceToMake = foundAChoiceToMake || offersAChoice
		if aboutCookiesOrConsent || offersAChoice {
			continue
		}
		for _, line := range strings.Split(block, "\n") {
			if normalisedTitle != "" && normalisedForConsentWallMatching(strings.TrimLeft(line, "# ")) == normalisedTitle {
				continue
			}
			charactersOutsideTheNotice += lettersAndDigitsOutsideMarkdownLinks(line)
		}
	}
	return foundCookieOrConsentWording && foundAChoiceToMake && charactersOutsideTheNotice < webPageMostlyLinksMaximumCharactersOutsideLinks
}

// markdownBlocksWithListsJoinedToTheirIntroduction splits markdown at
// blank lines, joining a block of list items to the block before it:
// "We use cookies to:" and the list of what for are one paragraph.
func markdownBlocksWithListsJoinedToTheirIntroduction(markdown string) []string {
	var blocks []string
	for _, block := range markdownBlankLine.Split(strings.TrimSpace(markdown), -1) {
		if len(blocks) > 0 && everyLineIsAListItem(block) {
			blocks[len(blocks)-1] += "\n" + block
			continue
		}
		blocks = append(blocks, block)
	}
	return blocks
}

func everyLineIsAListItem(block string) bool {
	for _, line := range strings.Split(block, "\n") {
		if strings.TrimSpace(line) != "" && !markdownListItemLine.MatchString(line) {
			return false
		}
	}
	return true
}

func normalisedForConsentWallMatching(text string) string {
	lowered := strings.ToLower(strings.NewReplacer("’", "'", "‘", "'").Replace(text))
	return strings.Join(strings.Fields(lowered), " ")
}

func containsAnyOf(text string, phrases []string) bool {
	for _, phrase := range phrases {
		if strings.Contains(text, phrase) {
			return true
		}
	}
	return false
}

func lettersAndDigitsOutsideMarkdownLinks(markdown string) int {
	count := 0
	for _, character := range markdownLinkOrImageWithItsText.ReplaceAllString(markdown, "") {
		if unicode.IsLetter(character) || unicode.IsDigit(character) {
			count++
		}
	}
	return count
}
