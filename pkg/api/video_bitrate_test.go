package api

import "testing"

func TestNewTrackerValidationSubjectProjectsVideoBitrateAssessment(t *testing.T) {
	assessment := VideoBitrateAssessment{Status: VideoBitrateStatusPresent, BitsPerSecond: 12_345_678}
	subject := NewTrackerValidationSubject(UploadSubject{Assessments: ReleaseAssessments{VideoBitrate: assessment}}, "EXAMPLE")
	if subject.Assessments.VideoBitrate != assessment {
		t.Fatalf("projected video bitrate = %#v, want %#v", subject.Assessments.VideoBitrate, assessment)
	}
}
