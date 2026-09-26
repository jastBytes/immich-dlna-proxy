package dlna

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/jastBytes/immich-dlna-proxy/immich"
)

// Timeline grouping modes (config.Config.TimelineGrouping).
const (
	TimelineGroupingNone  = "none"
	TimelineGroupingYear  = "year"
	TimelineGroupingMonth = "month"
)

// unknownDateBucket collects assets without a usable capture date.
const unknownDateBucket = "unknown"

var (
	yearBucketRE  = regexp.MustCompile(`^[0-9]{4}$`)
	monthBucketRE = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}$`)
)

// timelineBucket returns the year ("2024") or, with month set, the
// year-month ("2024-05") an asset's capture date falls in, or
// unknownDateBucket. The labels double as the container titles: they sort
// chronologically even on TVs that always sort by title themselves (see
// config.Config.TitleDatePrefix).
func timelineBucket(a immich.Asset, month bool) string {
	t := a.CapturedAt()
	if t.IsZero() {
		return unknownDateBucket
	}
	if month {
		return t.Format("2006-01")
	}
	return t.Format("2006")
}

// timelineGroup is one year/month container and its assets, in Immich's
// (newest-first) order.
type timelineGroup struct {
	bucket string
	assets []immich.Asset
}

// groupTimeline buckets media by year, or by year-month when month is set,
// optionally keeping only assets whose bucket starts with prefix (a year,
// to list that year's months). Groups come newest first, with the
// unknown-date group last.
func groupTimeline(media []immich.Asset, month bool, prefix string) []timelineGroup {
	idx := map[string]int{}
	var groups []timelineGroup
	for _, a := range media {
		b := timelineBucket(a, month)
		if prefix != "" && !strings.HasPrefix(b, prefix) {
			continue
		}
		i, ok := idx[b]
		if !ok {
			i = len(groups)
			idx[b] = i
			groups = append(groups, timelineGroup{bucket: b})
		}
		groups[i].assets = append(groups[i].assets, a)
	}
	slices.SortFunc(groups, func(x, y timelineGroup) int {
		switch {
		case x.bucket == unknownDateBucket:
			return 1
		case y.bucket == unknownDateBucket:
			return -1
		}
		return strings.Compare(y.bucket, x.bucket)
	})
	return groups
}

// browseTimeline handles "timeline" and, with TIMELINE_GROUPING set,
// its "timeline:<year>" and "timeline:<year>-<month>" sub-containers:
//
//	none:  timeline -> items (flat, newest first)
//	year:  timeline -> 2024, 2023, ... -> items
//	month: timeline -> 2024, ... -> 2024-12, 2024-11, ... -> items
//
// A flat list of thousands of items is unwieldy to scroll on a TV remote;
// grouping keeps every level short.
func (s *Server) browseTimeline(client cachedClient, userIdx int, childPrefix, local, rootSelfID string, args *browseArgs, baseURL string) (didl string, returned, total int, err error) {
	grouping := s.cfg.TimelineGrouping
	byMonth := grouping == TimelineGroupingMonth
	grouped := grouping == TimelineGroupingYear || byMonth

	bucket, isSub := strings.CutPrefix(local, "timeline:")
	if isSub {
		switch {
		case !grouped:
			return "", 0, 0, errNoSuchObject
		case bucket == unknownDateBucket, yearBucketRE.MatchString(bucket):
		case byMonth && monthBucketRE.MatchString(bucket):
		default:
			return "", 0, 0, errNoSuchObject
		}
	}

	if local == "timeline" && args.BrowseFlag == "BrowseMetadata" {
		// childCount omitted (-1): an accurate count means fetching the
		// whole library - see the root listing.
		return wrapDIDL(buildContainer(childPrefix+"timeline", rootSelfID, "Timeline", -1, "")), 1, 1, nil
	}

	assets, err := client.ListTimelineAssets()
	if err != nil {
		return "", 0, 0, fmt.Errorf("ListTimelineAssets: %w", err)
	}
	media := filterSupportedAssets(assets)
	sortReq := parseSortCriteria(args.SortCriteria)
	selfID := childPrefix + local

	// listItems renders the given assets as this container's children.
	listItems := func(items []immich.Asset) (string, int, int, error) {
		sortPhotos(items, sortReq)
		paged := page(items, args.StartingIndex, args.RequestedCount)
		var b strings.Builder
		for _, a := range paged {
			b.WriteString(buildAssetItem(baseURL, userIdx, childPrefix+"asset:"+a.ID, selfID, a, s.cfg.TitleDatePrefix, s.cfg.TitleDatePrefixDescending))
		}
		return wrapDIDL(b.String()), len(paged), len(items), nil
	}
	// listGroups renders groups as this container's child containers.
	listGroups := func(groups []timelineGroup) (string, int, int, error) {
		sortByTitle(groups, func(g timelineGroup) string { return g.bucket }, sortReq)
		paged := page(groups, args.StartingIndex, args.RequestedCount)
		var b strings.Builder
		for _, g := range paged {
			b.WriteString(buildContainer(childPrefix+"timeline:"+g.bucket, selfID, bucketTitle(g.bucket), len(g.assets), ""))
		}
		return wrapDIDL(b.String()), len(paged), len(groups), nil
	}

	if !isSub {
		if !grouped {
			return listItems(media)
		}
		return listGroups(groupTimeline(media, false, ""))
	}

	// A year or month container: find its assets.
	var mine []immich.Asset
	isMonth := monthBucketRE.MatchString(bucket)
	for _, a := range media {
		if timelineBucket(a, isMonth) == bucket {
			mine = append(mine, a)
		}
	}
	if len(mine) == 0 {
		return "", 0, 0, errNoSuchObject
	}

	if args.BrowseFlag == "BrowseMetadata" {
		parentID := childPrefix + "timeline"
		if isMonth {
			parentID += ":" + bucket[:4]
		}
		childCount := len(mine)
		if byMonth && !isMonth && bucket != unknownDateBucket {
			childCount = len(groupTimeline(mine, true, ""))
		}
		return wrapDIDL(buildContainer(selfID, parentID, bucketTitle(bucket), childCount, "")), 1, 1, nil
	}

	if byMonth && !isMonth && bucket != unknownDateBucket {
		return listGroups(groupTimeline(mine, true, bucket))
	}
	return listItems(mine)
}

// bucketTitle is the container title for a timeline bucket.
func bucketTitle(bucket string) string {
	if bucket == unknownDateBucket {
		return "Unknown date"
	}
	return bucket
}
