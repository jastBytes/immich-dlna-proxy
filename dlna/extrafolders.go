package dlna

import (
	"fmt"
	"math/rand/v2"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jastBytes/immich-dlna-proxy/immich"
)

// Optional root folders (config.Config.ExtraFolders, EXTRA_FOLDERS), shown
// after Albums/People/Timeline in the configured order.
const (
	folderFavorites = "favorites"
	folderOnThisDay = "onthisday"
	folderPlaces    = "places"
	folderRandom    = "random"
)

var extraFolderTitles = map[string]string{
	folderFavorites: "Favorites",
	folderOnThisDay: "On this day",
	folderPlaces:    "Places",
	folderRandom:    "Random",
}

// randomFolderSize is how many assets the Random folder shows.
const randomFolderSize = 100

// timeNow is time.Now, swappable in tests. "On this day" uses the local
// date (TZ; the embedded tzdata makes TZ work in the scratch image) and
// Random reshuffles once per hour.
var timeNow = time.Now

// rootChildCount is how many containers an account's root lists.
func (s *Server) rootChildCount() int {
	return 3 + len(s.cfg.ExtraFolders)
}

// extraRootContainers renders the enabled optional root folders. None of
// them report a childCount: each would need the whole library (or the
// favorites search) just to render the root listing.
func (s *Server) extraRootContainers(childPrefix, rootSelfID string) []string {
	out := make([]string, 0, len(s.cfg.ExtraFolders))
	for _, f := range s.cfg.ExtraFolders {
		out = append(out, buildContainer(childPrefix+f, rootSelfID, extraFolderTitles[f], -1, ""))
	}
	return out
}

// extraFolderFor returns which enabled optional folder local (an ObjectID
// relative to an account's root) belongs to, or "".
func (s *Server) extraFolderFor(local string) string {
	folder := local
	if strings.HasPrefix(local, "place:") {
		folder = folderPlaces
	}
	if slices.Contains(s.cfg.ExtraFolders, folder) {
		return folder
	}
	return ""
}

// browseExtraFolder handles the optional root folders and, for Places,
// their "place:<country>" and "place:<country>:<city>" sub-containers.
func (s *Server) browseExtraFolder(client cachedClient, userIdx int, childPrefix, local, rootSelfID string, args *browseArgs, baseURL string) (didl string, returned, total int, err error) {
	folder := s.extraFolderFor(local)
	if local == folder && args.BrowseFlag == "BrowseMetadata" {
		return wrapDIDL(buildContainer(childPrefix+local, rootSelfID, extraFolderTitles[folder], -1, "")), 1, 1, nil
	}

	var media []immich.Asset
	switch folder {
	case folderFavorites:
		assets, err := client.ListFavoriteAssets()
		if err != nil {
			return "", 0, 0, fmt.Errorf("ListFavoriteAssets: %w", err)
		}
		media = filterSupportedAssets(assets)
	case folderOnThisDay:
		assets, err := s.onThisDayAssets(client, timeNow())
		if err != nil {
			return "", 0, 0, err
		}
		media = filterSupportedAssets(assets)
	default:
		assets, err := client.ListTimelineAssets()
		if err != nil {
			return "", 0, 0, fmt.Errorf("ListTimelineAssets: %w", err)
		}
		media = filterSupportedAssets(assets)
	}

	selfID := childPrefix + local
	switch folder {
	case folderFavorites:
		return s.assetItems(media, args, baseURL, userIdx, childPrefix, selfID)
	case folderOnThisDay:
		return s.assetItems(media, args, baseURL, userIdx, childPrefix, selfID)
	case folderRandom:
		return s.assetItems(randomSample(media, timeNow(), userIdx), args, baseURL, userIdx, childPrefix, selfID)
	case folderPlaces:
		return s.browsePlaces(media, userIdx, childPrefix, local, args, baseURL)
	}
	return "", 0, 0, errNoSuchObject
}

// assetItems renders items as a container's children, sorted per the
// request's SortCriteria and paginated.
func (s *Server) assetItems(items []immich.Asset, args *browseArgs, baseURL string, userIdx int, childPrefix, parentID string) (string, int, int, error) {
	sortPhotos(items, parseSortCriteria(args.SortCriteria))
	paged := page(items, args.StartingIndex, args.RequestedCount)
	var b strings.Builder
	for _, a := range paged {
		b.WriteString(buildAssetItem(baseURL, userIdx, childPrefix+"asset:"+a.ID, parentID, a, s.itemOptions()))
	}
	return wrapDIDL(b.String()), len(paged), len(items), nil
}

// onThisDayConcurrency is how many per-year searches the "On this day"
// folder runs against Immich at once.
const onThisDayConcurrency = 4

// onThisDayMinYear bounds how far back "On this day" searches, so a single
// asset with a bogus capture date (year 1, say) can't turn one Browse into
// thousands of Immich requests.
const onThisDayMinYear = 1900

// onThisDayAssets returns the assets captured on today's month and day in
// earlier years, newest first, cached per day through the listing cache.
//
// It deliberately doesn't filter the full timeline listing the way Places
// and Random do: fetching every asset in a large library takes Immich
// longer than many TVs wait for a Browse response (a 33,000-asset library
// takes minutes to page through), and a TV that times out just shows the
// folder as empty. Instead it asks Immich for each earlier year's date
// directly (onThisDaySearch) - a handful of small searches, however large
// the library.
func (s *Server) onThisDayAssets(client cachedClient, now time.Time) ([]immich.Asset, error) {
	key := client.prefix + "onthisday:" + now.Format("2006-01-02")
	return cachedListing(client.cache, key, func() ([]immich.Asset, error) {
		return onThisDaySearch(client.client, now)
	})
}

// onThisDaySearch runs one capture-date search per earlier year, back to
// the library's oldest asset, onThisDayConcurrency at a time. Each search
// covers the date in every time zone (UTC-12 to UTC+14) and keeps only
// assets whose local capture date (Asset.LocalCaptureDate) is that day -
// the same rule Immich's own "On this day" memories use, so a photo taken
// shortly after midnight local time still counts for its local date.
func onThisDaySearch(c *immich.Client, now time.Time) ([]immich.Asset, error) {
	oldest, err := c.OldestAssetTime()
	if err != nil {
		return nil, fmt.Errorf("OldestAssetTime: %w", err)
	}
	if oldest.IsZero() {
		return nil, nil
	}
	month, day := now.Month(), now.Day()
	var years []int
	for y := now.Year() - 1; y >= max(oldest.Year()-1, onThisDayMinYear); y-- {
		if time.Date(y, month, day, 0, 0, 0, 0, time.UTC).Month() == month { // no 29 February in non-leap years
			years = append(years, y)
		}
	}

	results := make([][]immich.Asset, len(years))
	errs := make([]error, len(years))
	sem := make(chan struct{}, onThisDayConcurrency)
	var wg sync.WaitGroup
	for i, y := range years {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			start := time.Date(y, month, day, 0, 0, 0, 0, time.UTC)
			assets, err := c.ListAssetsTakenBetween(start.Add(-14*time.Hour), start.Add(36*time.Hour))
			if err != nil {
				errs[i] = fmt.Errorf("ListAssetsTakenBetween(%d-%02d-%02d): %w", y, month, day, err)
				return
			}
			for _, a := range assets {
				if ay, am, ad, ok := a.LocalCaptureDate(); ok && ay == y && am == month && ad == day {
					results[i] = append(results[i], a)
				}
			}
		}()
	}
	wg.Wait()

	var out []immich.Asset
	for i := range years {
		if errs[i] != nil {
			return nil, errs[i]
		}
		out = append(out, results[i]...)
	}
	return out, nil
}

// randomSample returns up to randomFolderSize assets picked at random from
// media. The pick is seeded by the current hour (and account), so it stays
// the same while a TV pages through the folder - reshuffling on every
// Browse call would show duplicates and skip assets between pages - and
// changes every hour.
func randomSample(media []immich.Asset, now time.Time, userIdx int) []immich.Asset {
	r := rand.New(rand.NewPCG(uint64(now.Unix()/3600), uint64(userIdx+2)))
	n := min(randomFolderSize, len(media))
	out := make([]immich.Asset, 0, n)
	for _, i := range r.Perm(len(media))[:n] {
		out = append(out, media[i])
	}
	return out
}

// placeID builds the ObjectID for a Places country ("place:<country>") or
// city ("place:<country>:<city>") container. Names are query-escaped so a
// ":" or "/" in them can't be confused with the separators; an asset with
// a country but no city goes in the country's city "" ("Other").
func placeID(childPrefix, country string, city *string) string {
	id := childPrefix + "place:" + url.QueryEscape(country)
	if city != nil {
		id += ":" + url.QueryEscape(*city)
	}
	return id
}

// parsePlaceID splits a "place:..." local ObjectID back into its country
// and, if present, city.
func parsePlaceID(local string) (country string, city *string, ok bool) {
	rest := strings.TrimPrefix(local, "place:")
	c, ci, hasCity := strings.Cut(rest, ":")
	country, err := url.QueryUnescape(c)
	if err != nil || country == "" {
		return "", nil, false
	}
	if !hasCity {
		return country, nil, true
	}
	cityName, err := url.QueryUnescape(ci)
	if err != nil {
		return "", nil, false
	}
	return country, &cityName, true
}

// cityTitle is the container title for a city bucket.
func cityTitle(city string) string {
	if city == "" {
		return "Other"
	}
	return city
}

// browsePlaces implements the Places folder: places -> one container per
// country -> one per city -> that city's items. Only geotagged assets
// Immich has reverse-geocoded (exifInfo.country set) appear.
func (s *Server) browsePlaces(media []immich.Asset, userIdx int, childPrefix, local string, args *browseArgs, baseURL string) (string, int, int, error) {
	byCountry := map[string]map[string][]immich.Asset{}
	for _, a := range media {
		country := strings.TrimSpace(a.ExifInfo.Country)
		if country == "" {
			continue
		}
		if byCountry[country] == nil {
			byCountry[country] = map[string][]immich.Asset{}
		}
		city := strings.TrimSpace(a.ExifInfo.City)
		byCountry[country][city] = append(byCountry[country][city], a)
	}
	sortReq := parseSortCriteria(args.SortCriteria)
	selfID := childPrefix + local

	// containers renders one child container per key, alphabetically
	// (or as SortCriteria asks), with the given title and child count.
	containers := func(keys []string, id func(string) string, title func(string) string, count func(string) int) (string, int, int, error) {
		slices.SortFunc(keys, func(a, b string) int { return strings.Compare(strings.ToLower(title(a)), strings.ToLower(title(b))) })
		sortByTitle(keys, title, sortReq)
		paged := page(keys, args.StartingIndex, args.RequestedCount)
		var b strings.Builder
		for _, k := range paged {
			b.WriteString(buildContainer(id(k), selfID, title(k), count(k), ""))
		}
		return wrapDIDL(b.String()), len(paged), len(keys), nil
	}

	if local == folderPlaces {
		countries := make([]string, 0, len(byCountry))
		for c := range byCountry {
			countries = append(countries, c)
		}
		return containers(countries,
			func(c string) string { return placeID(childPrefix, c, nil) },
			func(c string) string { return c },
			func(c string) int { return len(byCountry[c]) })
	}

	country, city, ok := parsePlaceID(local)
	if !ok || byCountry[country] == nil {
		return "", 0, 0, errNoSuchObject
	}
	cities := byCountry[country]

	if city == nil { // a country
		if args.BrowseFlag == "BrowseMetadata" {
			return wrapDIDL(buildContainer(selfID, childPrefix+folderPlaces, country, len(cities), "")), 1, 1, nil
		}
		keys := make([]string, 0, len(cities))
		for c := range cities {
			keys = append(keys, c)
		}
		return containers(keys,
			func(c string) string { return placeID(childPrefix, country, &c) },
			cityTitle,
			func(c string) int { return len(cities[c]) })
	}

	items, ok := cities[*city]
	if !ok {
		return "", 0, 0, errNoSuchObject
	}
	if args.BrowseFlag == "BrowseMetadata" {
		return wrapDIDL(buildContainer(selfID, placeID(childPrefix, country, nil), cityTitle(*city), len(items), "")), 1, 1, nil
	}
	return s.assetItems(items, args, baseURL, userIdx, childPrefix, selfID)
}
