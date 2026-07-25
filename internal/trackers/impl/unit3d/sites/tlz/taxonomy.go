package tlz

import (
	"strings"

	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

func typeID(meta api.UploadSubject) string {
	if strings.EqualFold(unit3d.Category(meta), "MOVIE") {
		return "1"
	}
	if meta.TVPack {
		return "4"
	}
	return "3"
}
