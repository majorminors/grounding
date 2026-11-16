package tools

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// Pages that are nothing but a cookie or consent notice, in the shapes
// seen: a forum thread whose browser render was its title and the
// notice (served twice, as one was on 2026-11-08), and the banner of a
// consent-management platform with no page behind it.
var consentNoticePagesServedInPlaceOfThePage = map[string]string{
	"forum-thread": `<html><head><title>How do you keep inventory of your food? : r/pantry</title></head><body>
<h1>How do you keep inventory of your food? : r/pantry</h1>` + strings.Repeat(`
<div role="dialog"><h2>Let us know your cookie preferences</h2>
<p>This forum uses cookies and similar technologies to:</p>
<ul><li>Keep the website operational and running properly</li><li>Prevent fraud and abuse</li><li>Monitor site usage and performance metrics</li></ul>
<p>If you choose “Accept All” we’ll also use cookies to:</p>
<ul><li>Personalise your recommendations and ads</li><li>Measure the performance of ads</li><li>Develop and improve new features and services</li></ul>
<p>If you choose “Reject Optional Cookies” we won’t use cookies for these additional uses. You can update your cookie preference from your settings.</p>
<p>Learn more about <a href="/policies/cookies">how we use cookies</a>.</p>
<button>Accept All</button> <button>Reject Optional Cookies</button></div>`, 2) + `
</body></html>`,
	"consent-platform-banner": `<html><head><title>Daily Courier</title></head><body>
<div id="consent-banner"><h2>We value your privacy</h2>
<p>We and our 812 partners store and access information on your device, such as cookies, and process personal data such as unique identifiers for personalised advertising and content, ad and content measurement, audience research and service development.</p>
<p>By clicking “Accept all” you consent to this processing. You may refuse, or set your preferences, at any time by choosing “Customise”.</p>
<button>Accept all</button> <button>Reject all</button> <button>Customise</button></div>
</body></html>`,
}

func TestWebFetchRefusesAPageThatIsOnlyACookieOrConsentNotice(t *testing.T) {
	for name, page := range consentNoticePagesServedInPlaceOfThePage {
		readers, base := testPageReaders(t, servesHTML(page))
		_, err := readers.fetchWebPage(context.Background(), WebFetchOptions{URL: base + "/thread", Renderer: WebFetchRendererPlain})
		if err == nil || !strings.Contains(err.Error(), "only the site's cookie or consent notice") || !strings.Contains(err.Error(), `renderer "auto"`) {
			t.Errorf("%s: want a refusal naming the consent notice and the auto renderer, got %v", name, err)
		}
		_, err = readers.fetchWebPage(context.Background(), WebFetchOptions{URL: base + "/thread"})
		if err == nil || !strings.Contains(err.Error(), "no renderer could read the page") || !strings.Contains(err.Error(), "cookie or consent notice") {
			t.Errorf("%s: want auto to say no renderer could read it, and why, got %v", name, err)
		}
	}
}

// Pages that mention cookies or consent and must still be read.
var pagesThatMentionCookiesOrConsentAndAreThePage = map[string]string{
	// The content loaded beneath its banner.
	"article-under-its-banner": strings.Replace(articleHTML, "</body>",
		`<div role="dialog"><p>We use cookies to improve your experience. By clicking “Accept all” you agree to our use of cookies.</p><button>Accept all</button> <button>Reject all</button></div></body>`, 1),
	// A short clinical abstract: consent, agreement and acceptance, but
	// no cookie notice.
	"clinical-abstract": `<html><head><title>A trial of morning light</title></head><body><article><h1>A trial of morning light</h1>
<p>Forty adults with delayed sleep phase gave informed consent and agreed to thirty minutes of bright light each morning for four weeks. Sleep onset moved earlier by a median of 41 minutes; participants rated the routine acceptable, and none withdrew. Consent forms were approved by the ethics committee.</p></article></body></html>`,
	// A short page about cookies that asks for no choice.
	"page-about-cookies": `<html><head><title>About cookies</title></head><body><article><h1>About cookies</h1>
<p>A cookie is a small file a website stores in your browser so that it can recognise you on a later visit. Session cookies vanish when the browser closes; persistent cookies stay until they expire.</p></article></body></html>`,
}

func TestWebFetchReadsPagesThatOnlyMentionCookiesOrConsent(t *testing.T) {
	for name, page := range pagesThatMentionCookiesOrConsentAndAreThePage {
		readers, base := testPageReaders(t, servesHTML(page))
		if _, err := readers.fetchWebPage(context.Background(), WebFetchOptions{URL: base + "/page", Renderer: WebFetchRendererPlain}); err != nil {
			t.Errorf("%s: want the page read, got %v", name, err)
		}
	}
}

func TestAutoClimbsPastACrawl4AIConsentNoticeToFirecrawl(t *testing.T) {
	setUp := &ladderSetUp{
		site:           answersStatus(http.StatusForbidden),
		crawl4aiResult: map[string]any{"success": true, "status_code": 200, "html": consentNoticePagesServedInPlaceOfThePage["forum-thread"], "redirected_url": ladderTestPageURL},
		firecrawlData:  firecrawlReadTheArticle,
	}
	out, err := setUp.readers(t).fetchWebPage(context.Background(), WebFetchOptions{URL: ladderTestPageURL})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Renderer: firecrawl", "crawl4ai was served only the site's cookie or consent notice, not the page", "means-ends analysis"} {
		if !strings.Contains(out, want) {
			t.Errorf("want %q:\n%s", want, out)
		}
	}
}

func TestAutoSaysSoWhenEveryRungIsServedOnlyAConsentNotice(t *testing.T) {
	setUp := &ladderSetUp{
		site:           answersStatus(http.StatusForbidden),
		crawl4aiResult: map[string]any{"success": true, "status_code": 200, "html": consentNoticePagesServedInPlaceOfThePage["forum-thread"], "redirected_url": ladderTestPageURL},
	}
	_, err := setUp.readers(t).fetchWebPage(context.Background(), WebFetchOptions{URL: ladderTestPageURL})
	if err == nil || !strings.Contains(err.Error(), "crawl4ai was served only the site's cookie or consent notice") {
		t.Errorf("want the caller told the page was only a consent notice, got %v", err)
	}
}
