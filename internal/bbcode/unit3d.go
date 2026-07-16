// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bbcode

import descriptionunit3d "github.com/autobrr/upbrr/internal/description/unit3d"

// CleanUnit3DDescription normalizes a Unit3D description for site and converts its cleanup report.
func CleanUnit3DDescription(description string, site string) Report {
	report := descriptionunit3d.CleanDescription(description, site)
	return Report{
		Description: report.Description,
		Images:      convertUnit3DImages(report.Images),
		Notes:       convertUnit3DNotes(report.Notes),
	}
}

func convertUnit3DImages(images []descriptionunit3d.Image) []Image {
	if len(images) == 0 {
		return nil
	}
	converted := make([]Image, 0, len(images))
	for _, image := range images {
		converted = append(converted, Image{
			ImgURL: image.ImgURL,
			RawURL: image.RawURL,
			WebURL: image.WebURL,
			Host:   image.Host,
		})
	}
	return converted
}

func convertUnit3DNotes(notes []descriptionunit3d.Note) []Note {
	if len(notes) == 0 {
		return nil
	}
	converted := make([]Note, 0, len(notes))
	for _, note := range notes {
		converted = append(converted, Note{
			Kind:    note.Kind,
			Message: note.Message,
		})
	}
	return converted
}
