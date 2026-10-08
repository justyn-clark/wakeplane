package main

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
)

type githubRepository struct {
	FullName    string `json:"full_name"`
	HTMLURL     string `json:"html_url"`
	Description string `json:"description"`
	PushedAt    string `json:"pushed_at"`
	Stars       int    `json:"stargazers_count"`
	OpenIssues  int    `json:"open_issues_count"`
}

type githubRelease struct {
	Name        string `json:"name"`
	TagName     string `json:"tag_name"`
	HTMLURL     string `json:"html_url"`
	PublishedAt string `json:"published_at"`
}

type githubPull struct {
	Number    int    `json:"number"`
	Title     string `json:"title"`
	HTMLURL   string `json:"html_url"`
	UpdatedAt string `json:"updated_at"`
}

func (r *runner) repositoryReport(ctx context.Context, repository string) (report, error) {
	base := strings.TrimRight(r.config.GitHubURL, "/") + "/repos/" + repository
	var metadata githubRepository
	if _, err := r.fetchJSON(ctx, base, &metadata, false); err != nil {
		return report{}, fmt.Errorf("repository information: %w", err)
	}
	if metadata.FullName == "" {
		return report{}, errors.New("repository information omitted its identity")
	}
	result := report{
		Title: "Repository watch: " + repository, GeneratedAt: time.Now().UTC(),
		Summary: fmt.Sprintf("%s. %s (including pull requests). Last push: %s.", countLabel(metadata.Stars, "star", "stars"), countLabel(metadata.OpenIssues, "open issue", "open issues"), reportDate(truncate(metadata.PushedAt, 80))),
		Items: []reportItem{{
			Title: "Repository overview", URL: outputURL(metadata.HTMLURL, base), Source: repository,
			PublishedAt: truncate(metadata.PushedAt, 80), Excerpt: plainText(metadata.Description, 500),
		}},
	}
	var release githubRelease
	found, err := r.fetchJSON(ctx, base+"/releases/latest", &release, true)
	if err != nil {
		return report{}, fmt.Errorf("latest release: %w", err)
	}
	if found {
		name := release.Name
		if name == "" {
			name = release.TagName
		}
		result.Items = append(result.Items, reportItem{
			Title: "Latest stable release: " + plainText(name, 180), URL: outputURL(release.HTMLURL, base),
			Source: repository, PublishedAt: truncate(release.PublishedAt, 80),
		})
	} else {
		result.Summary += " No published stable release."
	}
	var pulls []githubPull
	if _, err := r.fetchJSON(ctx, base+"/pulls?state=open&sort=updated&direction=desc&per_page=5", &pulls, false); err != nil {
		return report{}, fmt.Errorf("open pull requests: %w", err)
	}
	if len(pulls) > 5 {
		pulls = pulls[:5]
	}
	for _, pull := range pulls {
		result.Items = append(result.Items, reportItem{
			Title: fmt.Sprintf("PR #%d: %s", pull.Number, plainText(pull.Title, 180)),
			URL:   outputURL(pull.HTMLURL, base), Source: repository, PublishedAt: truncate(pull.UpdatedAt, 80),
		})
	}
	if len(pulls) > 0 {
		result.Summary += fmt.Sprintf(" Showing %s.", countLabel(len(pulls), "recent pull request", "recent pull requests"))
	}
	return result, nil
}

func (r *runner) fetchJSON(ctx context.Context, address string, target any, allowNotFound bool) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2026-03-10")
	req.Header.Set("User-Agent", "wakeplane-automation-runner")
	if r.config.GitHubToken != "" {
		req.Header.Set("Authorization", "Bearer "+r.config.GitHubToken)
	}
	response, err := r.client.Do(req)
	if err != nil {
		return false, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound && allowNotFound {
		return false, nil
	}
	if response.StatusCode != http.StatusOK {
		return false, fmt.Errorf("source returned HTTP %d", response.StatusCode)
	}
	data, err := readSource(response.Body)
	if err != nil {
		return false, err
	}
	if err := json.Unmarshal(data, target); err != nil {
		return false, fmt.Errorf("source returned invalid JSON: %w", err)
	}
	return true, nil
}

type rssDocument struct {
	XMLName xml.Name `xml:"rss"`
	Channel struct {
		Title string `xml:"title"`
		Items []struct {
			Title       string `xml:"title"`
			Link        string `xml:"link"`
			PublishedAt string `xml:"pubDate"`
			Description string `xml:"description"`
		} `xml:"item"`
	} `xml:"channel"`
}

type atomDocument struct {
	XMLName xml.Name `xml:"feed"`
	Title   string   `xml:"title"`
	Entries []struct {
		Title string `xml:"title"`
		Links []struct {
			Href string `xml:"href,attr"`
			Rel  string `xml:"rel,attr"`
		} `xml:"link"`
		PublishedAt string `xml:"published"`
		UpdatedAt   string `xml:"updated"`
		Summary     string `xml:"summary"`
		Content     string `xml:"content"`
	} `xml:"entry"`
}

func (r *runner) feedReport(ctx context.Context, addresses []string) (report, error) {
	result := report{Title: "Weekly reading summary", GeneratedAt: time.Now().UTC(), Items: []reportItem{}}
	seen := make(map[string]bool)
	for _, address := range addresses {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
		if err != nil {
			return report{}, err
		}
		req.Header.Set("Accept", "application/rss+xml, application/atom+xml, application/xml, text/xml")
		req.Header.Set("User-Agent", "wakeplane-automation-runner")
		response, err := r.client.Do(req)
		if err != nil {
			return report{}, fmt.Errorf("read feed %s: %w", address, err)
		}
		data, readErr := readSource(response.Body)
		_ = response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return report{}, fmt.Errorf("feed %s returned HTTP %d", address, response.StatusCode)
		}
		if readErr != nil {
			return report{}, fmt.Errorf("feed %s: %w", address, readErr)
		}
		items, err := parseFeed(data, address)
		if err != nil {
			return report{}, fmt.Errorf("feed %s: %w", address, err)
		}
		for _, item := range items {
			key := item.URL
			if key == "" {
				key = item.Source + "\x00" + item.Title
			}
			if seen[key] {
				continue
			}
			seen[key] = true
			result.Items = append(result.Items, item)
		}
	}
	sort.SliceStable(result.Items, func(i, j int) bool {
		return parsedFeedTime(result.Items[i].PublishedAt).After(parsedFeedTime(result.Items[j].PublishedAt))
	})
	if len(result.Items) > maxArticles {
		result.Items = result.Items[:maxArticles]
	}
	result.Summary = fmt.Sprintf("%s from %s.", countLabel(len(result.Items), "recent entry", "recent entries"), countLabel(len(addresses), "feed", "feeds"))
	return result, nil
}

func countLabel(count int, singular, plural string) string {
	if count == 1 {
		return fmt.Sprintf("%d %s", count, singular)
	}
	return fmt.Sprintf("%d %s", count, plural)
}

func parseFeed(data []byte, address string) ([]reportItem, error) {
	var root struct{ XMLName xml.Name }
	if err := xml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("invalid XML: %w", err)
	}
	items := []reportItem{}
	switch root.XMLName.Local {
	case "rss":
		var feed rssDocument
		if err := xml.Unmarshal(data, &feed); err != nil {
			return nil, err
		}
		for _, item := range feed.Channel.Items {
			items = append(items, reportItem{
				Title: plainText(item.Title, 200), URL: outputURL(item.Link, address),
				Source: plainText(feed.Channel.Title, 200), PublishedAt: truncate(item.PublishedAt, 80), Excerpt: plainText(item.Description, 320),
			})
		}
	case "feed":
		var feed atomDocument
		if err := xml.Unmarshal(data, &feed); err != nil {
			return nil, err
		}
		for _, item := range feed.Entries {
			link := ""
			for _, candidate := range item.Links {
				if candidate.Rel == "alternate" || candidate.Rel == "" {
					link = candidate.Href
					break
				}
			}
			published := item.PublishedAt
			if published == "" {
				published = item.UpdatedAt
			}
			excerpt := item.Summary
			if excerpt == "" {
				excerpt = item.Content
			}
			items = append(items, reportItem{
				Title: plainText(item.Title, 200), URL: outputURL(link, address),
				Source: plainText(feed.Title, 200), PublishedAt: truncate(published, 80), Excerpt: plainText(excerpt, 320),
			})
		}
	default:
		return nil, errors.New("expected an RSS or Atom feed")
	}
	return items, nil
}

func parsedFeedTime(value string) time.Time {
	for _, layout := range []string{time.RFC3339, time.RFC1123Z, time.RFC1123, time.RFC822Z, time.RFC822} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed
		}
	}
	return time.Time{}
}

func readSource(reader io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, maxSourceBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxSourceBytes {
		return nil, fmt.Errorf("source exceeds %d bytes", maxSourceBytes)
	}
	return data, nil
}

var htmlTag = regexp.MustCompile(`<[^>]*>`)

func plainText(value string, limit int) string {
	return truncate(strings.Join(strings.Fields(html.UnescapeString(htmlTag.ReplaceAllString(value, " "))), " "), limit)
}

func truncate(value string, limit int) string {
	runes := []rune(value)
	if len(runes) > limit {
		return string(runes[:limit]) + "…"
	}
	return value
}

func outputURL(value, base string) string {
	if value == "" {
		return ""
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return ""
	}
	baseURL, err := validHTTPURL(base)
	if err != nil {
		return ""
	}
	resolved := baseURL.ResolveReference(parsed).String()
	link, err := url.Parse(resolved)
	if err != nil {
		return ""
	}
	link.Fragment = ""
	if _, err := validHTTPURL(link.String()); err != nil || len(resolved) > 2048 {
		return ""
	}
	return resolved
}

func renderReport(result report) string {
	escape := strings.NewReplacer("\\", "\\\\", "[", "\\[", "]", "\\]", "*", "\\*", "#", "\\#", "<", "&lt;", ">", "&gt;")
	var builder strings.Builder
	fmt.Fprintf(&builder, "# %s\n\n%s\n\n%s\n\n", escape.Replace(asciiText(result.Title)), reportDate(result.GeneratedAt.Format(time.RFC3339)), escape.Replace(asciiText(result.Summary)))
	if result.Delivery != nil {
		fmt.Fprintf(&builder, "Notification: %s (%d attempts)\n\n", result.Delivery.Status, result.Delivery.Attempts)
	}
	for _, item := range result.Items {
		title := escape.Replace(asciiText(item.Title))
		if address := asciiURL(item.URL); address != "" {
			// Angle-bracket destinations keep parentheses in a source URL from
			// changing Markdown's link syntax. Brackets are percent-encoded.
			link := strings.NewReplacer("<", "%3C", ">", "%3E").Replace(address)
			fmt.Fprintf(&builder, "- [%s](<%s>) - %s", title, link, escape.Replace(asciiText(item.Source)))
		} else {
			fmt.Fprintf(&builder, "- %s - %s", title, escape.Replace(asciiText(item.Source)))
		}
		if item.PublishedAt != "" {
			fmt.Fprintf(&builder, " (%s)", escape.Replace(reportDate(item.PublishedAt)))
		}
		fmt.Fprintln(&builder)
		if item.Excerpt != "" {
			fmt.Fprintf(&builder, "  %s\n", escape.Replace(asciiText(item.Excerpt)))
		}
		fmt.Fprintln(&builder)
	}
	return builder.String()
}
