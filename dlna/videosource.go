package dlna

import "log"

// Video source modes (config.Config.VideoSource, VIDEO_SOURCE).
const (
	// VideoSourceOriginal serves every video as its original file.
	VideoSourceOriginal = "original"
	// VideoSourceTranscoded serves Immich's playback version of every
	// video (GET /api/assets/{id}/video/playback): the file Immich
	// transcoded under its own transcoding policy - H.264/AAC in MP4 by
	// default, which virtually every TV can play - or the original where
	// Immich didn't transcode it. Immich does the transcoding; the proxy
	// never re-encodes anything itself.
	VideoSourceTranscoded = "transcoded"
)

// mediaVariant is which of an asset's renditions /media/ serves.
type mediaVariant int

const (
	variantOriginal mediaVariant = iota
	variantPreview               // Immich's JPEG preview (PHOTO_SOURCE)
	variantPlayback              // Immich's video playback version (VIDEO_SOURCE)
)

// servesTranscoded reports whether videos are served as Immich's playback
// version (VIDEO_SOURCE=transcoded).
func (s *Server) servesTranscoded() bool {
	return s.cfg.VideoSource == VideoSourceTranscoded
}

// mediaVariantFor decides which rendition /media/ serves for an asset on a
// cache miss - the same decision buildAssetItem made when it advertised
// the item. With both PHOTO_SOURCE=original and VIDEO_SOURCE=original
// that's always the original, without asking Immich anything. Otherwise
// it needs the asset's type and MIME type, so it looks the asset up
// (through the listing cache, which the Browse that listed it has usually
// just filled). If the lookup fails, it falls back to the original file
// rather than failing the request.
func (s *Server) mediaVariantFor(userIdx int, assetID string) mediaVariant {
	photoOriginal := s.cfg.PhotoSource == "" || s.cfg.PhotoSource == PhotoSourceOriginal
	if photoOriginal && !s.servesTranscoded() {
		return variantOriginal
	}
	a, err := s.cachedClient(userIdx).GetAsset(assetID)
	if err != nil {
		log.Printf("GetAsset(%s) failed, serving the original: %v", assetID, err)
		return variantOriginal
	}
	switch {
	case a.IsVideo() && s.servesTranscoded():
		return variantPlayback
	case servesPreview(s.cfg.PhotoSource, *a):
		return variantPreview
	}
	return variantOriginal
}
