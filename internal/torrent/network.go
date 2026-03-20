package torrent

import (
	"context"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	libtorrent "github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
)

var trackerHostCache sync.Map

var kinozalTrackerIPs = map[string]string{
	".torrent4me.com": "85.17.248.14",
	".tor4me.info":    "212.92.98.247",
}

var defaultPublicTrackers = [][]string{
	{
		"udp://tracker.opentrackr.org:1337/announce",
		"udp://open.demonii.com:1337/announce",
		"udp://open.stealth.si:80/announce",
		"https://torrent.tracker.durukanbal.com:443/announce",
		"udp://wepzone.net:6969/announce",
	},
	{
		"udp://tracker.wepzone.net:6969/announce",
		"udp://tracker.torrent.eu.org:451/announce",
		"udp://tracker.theoks.net:6969/announce",
		"udp://tracker.t-1.org:6969/announce",
		"udp://tracker.darkness.services:6969/announce",
	},
	{
		"udp://tracker-udp.gbitt.info:80/announce",
		"udp://t.overflow.biz:6969/announce",
		"udp://open.dstud.io:6969/announce",
		"udp://explodie.org:6969/announce",
		"udp://exodus.desync.com:6969/announce",
	},
}

func addTorrentSource(tc *libtorrent.Client, source string) (*libtorrent.Torrent, string, error) {
	spec, name, err := torrentSpecFromSource(source)
	if err != nil {
		return nil, "", err
	}

	t, _, err := tc.AddTorrentSpec(spec)
	if err != nil {
		return nil, "", err
	}

	if strings.TrimSpace(name) == "" {
		name = strings.TrimSpace(t.Name())
	}

	return t, name, nil
}

func torrentSpecFromSource(source string) (*libtorrent.TorrentSpec, string, error) {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(source)), "magnet:") {
		spec, err := libtorrent.TorrentSpecFromMagnetUri(source)
		if err == nil && len(spec.Trackers) == 0 {
			spec.Trackers = defaultPublicTrackers
		}
		return spec, "", err
	}

	mi, err := metainfo.LoadFromFile(source)
	if err != nil {
		return nil, "", err
	}

	spec, err := libtorrent.TorrentSpecFromMetaInfoErr(mi)
	if err != nil {
		return nil, "", err
	}

	name := ""
	usePublicFallback := true
	if info, err := mi.UnmarshalInfo(); err == nil {
		name = strings.TrimSpace(info.BestName())
		if info.Private != nil && *info.Private {
			usePublicFallback = false
		}
	}

	spec.Trackers = effectiveTrackerList(mi.UpvertedAnnounceList(), usePublicFallback)
	return spec, name, nil
}

func trackerDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: 12 * time.Second}

	host, port, err := net.SplitHostPort(addr)
	if err == nil {
		if _, ok := kinozalTrackerPrefix(host); ok {
			if ip, ok := resolveKinozalTrackerHost(ctx, host); ok {
				addr = net.JoinHostPort(ip, port)
			}
		}
	}

	return dialer.DialContext(ctx, network, addr)
}

func resolveKinozalTrackerHost(ctx context.Context, host string) (string, bool) {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return "", false
	}

	if cached, ok := trackerHostCache.Load(host); ok {
		if ip, ok := cached.(string); ok && ip != "" {
			return ip, true
		}
	}

	if parsed := net.ParseIP(host); parsed != nil {
		ip := parsed.String()
		trackerHostCache.Store(host, ip)
		return ip, true
	}

	for suffix, ip := range kinozalTrackerIPs {
		if strings.HasSuffix(host, suffix) {
			trackerHostCache.Store(host, ip)
			return ip, true
		}
	}

	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err == nil {
		for _, addr := range addrs {
			if ipv4 := addr.IP.To4(); ipv4 != nil {
				ip := ipv4.String()
				trackerHostCache.Store(host, ip)
				return ip, true
			}
		}
	}

	return "", false
}

func effectiveTrackerList(announceList [][]string, usePublicFallback bool) [][]string {
	groups := [][][]string{
		kinozalFallbackTrackers(announceList),
		announceList,
	}
	if usePublicFallback {
		groups = append(groups, defaultPublicTrackers)
	}
	return combineTrackerLists(groups...)
}

func applyEffectiveTrackers(t *libtorrent.Torrent) {
	if t == nil {
		return
	}

	// add more trackers
	meta := t.Metainfo()
	usePublicFallback := true
	if info := t.Info(); info != nil && info.Private != nil && *info.Private {
		usePublicFallback = false
	}
	trackers := effectiveTrackerList(meta.UpvertedAnnounceList(), usePublicFallback)
	if len(trackers) == 0 {
		return
	}

	t.AddTrackers(trackers)
}

func combineTrackerLists(groups ...[][]string) [][]string {
	seen := make(map[string]struct{})
	out := make([][]string, 0)

	for _, group := range groups {
		for _, tier := range group {
			cleaned := make([]string, 0, len(tier))
			for _, tracker := range tier {
				tracker = strings.TrimSpace(tracker)
				if tracker == "" {
					continue
				}
				if _, ok := seen[tracker]; ok {
					continue
				}
				seen[tracker] = struct{}{}
				cleaned = append(cleaned, tracker)
			}
			if len(cleaned) > 0 {
				out = append(out, cleaned)
			}
		}
	}

	return out
}

func kinozalFallbackTrackers(announceList [][]string) [][]string {
	trackers := make([]string, 0)
	seen := make(map[string]struct{})

	for _, tier := range announceList {
		for _, raw := range tier {
			parsed, err := url.Parse(strings.TrimSpace(raw))
			if err != nil {
				continue
			}
			if strings.TrimSpace(parsed.Query().Get("uk")) == "" {
				continue
			}
			if _, ok := kinozalTrackerPrefix(parsed.Hostname()); !ok {
				continue
			}

			ip, ok := resolveKinozalTrackerHost(context.Background(), parsed.Hostname())
			if !ok {
				continue
			}

			host := ip
			if port := parsed.Port(); port != "" {
				host = net.JoinHostPort(ip, port)
			}

			copy := *parsed
			copy.Host = host
			tracker := copy.String()
			if _, ok := seen[tracker]; ok {
				continue
			}
			seen[tracker] = struct{}{}
			trackers = append(trackers, tracker)
		}
	}

	if len(trackers) == 0 {
		return nil
	}

	return [][]string{trackers}
}

func kinozalTrackerPrefix(host string) (string, bool) {
	host = strings.ToLower(strings.TrimSpace(host))

	for _, suffix := range []string{
		".torrent4me.com",
		".tor4me.info",
		".tor2me.info",
		".kinozal-tv.appspot.com",
	} {
		if !strings.HasSuffix(host, suffix) {
			continue
		}

		prefix := strings.TrimSuffix(host, suffix)
		if len(prefix) < 3 || !strings.HasPrefix(prefix, "tr") {
			return "", false
		}
		if _, err := strconv.Atoi(strings.TrimPrefix(prefix, "tr")); err != nil {
			return "", false
		}
		return prefix, true
	}

	return "", false
}

func peerCount(t *libtorrent.Torrent) int {
	if t == nil {
		return 0
	}

	stats := t.Stats()
	count := len(t.KnownSwarm())

	for _, candidate := range []int{
		stats.ActivePeers,
		stats.PendingPeers,
		stats.HalfOpenPeers,
		stats.TotalPeers,
	} {
		if candidate > count {
			count = candidate
		}
	}

	return count
}
