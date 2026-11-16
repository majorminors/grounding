package tools

import (
	"regexp"
	"strings"
	"unicode"
)

// A site that suspects a robot often answers 200 with a bot check or a
// block page instead of the page, and grounding used to return that as
// the page's text: on 2026-11-07 the page-reading bake-off (LOG) found
// web_fetch handing back "Checking your browser before accessing
// pmc.ncbi.nlm.nih.gov" as a PubMed Central article, and Nature's and
// JSTOR's "A required part of this site couldn't load", with nothing
// said; Firecrawl returned Quora's "Blocked by network security" the
// same way. A researcher read those and moved on.

// botCheckDetectionMaximumCharacters bounds where the phrases are looked
// for: a bot check is a short page, while an article about Cloudflare
// may quote one in passing. The bake-off's were 186 to 865 characters.
const botCheckDetectionMaximumCharacters = 2000

// botCheckAndBlockPagePhrases are each the words of one anti-bot
// service's or site's challenge or block page, lower case, apostrophes
// straight. The first three are the bake-off's; the rest are the
// widely seen pages of Cloudflare, DataDome, PerimeterX, Akamai,
// Imperva and Google.
var botCheckAndBlockPagePhrases = []string{
	"checking your browser before accessing",
	"a required part of this site couldn't load",
	"blocked by network security",
	"enable javascript and cookies to continue",
	"verify you are human",
	"verifying you are human",
	"attention required! | cloudflare",
	"please enable js and disable any ad blocker",
	"to confirm you are a human",
	"pardon our interruption",
	"you don't have permission to access",
	"incapsula incident id",
	"unusual traffic from your computer network",
}

// botCheckOrBlockPagePhrase returns the phrase that marks text as a bot
// check or block page, or "" when it is not one.
func botCheckOrBlockPagePhrase(text string) string {
	if len([]rune(text)) > botCheckDetectionMaximumCharacters {
		return ""
	}
	searchable := strings.ToLower(strings.NewReplacer("’", "'", "‘", "'").Replace(text))
	searchable = strings.Join(strings.Fields(searchable), " ")
	for _, phrase := range botCheckAndBlockPagePhrases {
		if strings.Contains(searchable, phrase) {
			return phrase
		}
	}
	return ""
}

// webPageMostlyLinksMaximumCharactersOutsideLinks: a page with fewer
// letters and digits than this outside its links is nearly all links
// (YouTube's player page read without JavaScript is its footer; a
// sign-in wall is a few buttons), so the caller is told.
const webPageMostlyLinksMaximumCharactersOutsideLinks = 200

var markdownLinkOrImageWithItsText = regexp.MustCompile(`!?\[[^\]]*\]\([^)]*\)`)

// webPageIsMostlyLinks reports a page that has text but almost none of
// it outside links.
func webPageIsMostlyLinks(markdown string) bool {
	if strings.TrimSpace(markdown) == "" {
		return false
	}
	outsideLinks := markdownLinkOrImageWithItsText.ReplaceAllString(markdown, "")
	count := 0
	for _, character := range outsideLinks {
		if unicode.IsLetter(character) || unicode.IsDigit(character) {
			count++
		}
	}
	return count < webPageMostlyLinksMaximumCharactersOutsideLinks
}
