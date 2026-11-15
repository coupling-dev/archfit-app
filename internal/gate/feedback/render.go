package feedback

// Annotation caps: GitHub takes at most 50 annotations in one check-run request; more go in further updates.
const (
	annotationsPerRequest = 50
	annotationCap         = 150 // in total, so one publication makes at most three requests per check
)
