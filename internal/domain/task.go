package domain

import "time"

// Strategy is the download strategy chosen for a task.
type Strategy string

// Download strategies.
const (
	StrategySingle       Strategy = "single"
	StrategyMultipart    Strategy = "multipart"
	StrategyConservative Strategy = "conservative"
	StrategyResume       Strategy = "resume"
	StrategyUnsupported  Strategy = "unsupported"
)

// ExistingFilePolicy says what to do when the destination already exists.
type ExistingFilePolicy string

// Existing-file policies.
const (
	PolicyRename    ExistingFilePolicy = "rename"
	PolicyOverwrite ExistingFilePolicy = "overwrite"
	PolicyFail      ExistingFilePolicy = "fail"
)

// NetworkPolicy controls which destination addresses may be dialled.
type NetworkPolicy string

// Network policies.
const (
	NetAllowDirectPrivate NetworkPolicy = "allow-direct-private"
	NetAllowAll           NetworkPolicy = "allow-all"
	NetBlockPrivate       NetworkPolicy = "block-private"
)

// ValidatorKind is the strength of the validator available for a resource.
type ValidatorKind string

// Validator kinds.
const (
	ValidatorStrongETag   ValidatorKind = "strong-etag"
	ValidatorLastModified ValidatorKind = "last-modified"
	ValidatorNone         ValidatorKind = "none"
)

// Validators identify a specific version of a remote resource.
type Validators struct {
	ETag         string
	ETagStrong   bool
	LastModified string
}

// Kind reports the strongest validator available.
func (v Validators) Kind() ValidatorKind {
	switch {
	case v.ETag != "" && v.ETagStrong:
		return ValidatorStrongETag
	case v.LastModified != "":
		return ValidatorLastModified
	default:
		return ValidatorNone
	}
}

// Matches reports whether o identifies the same resource version. Strong
// ETags compare exactly; otherwise a non-empty equal Last-Modified is
// required. With no usable validator it returns false.
func (v Validators) Matches(o Validators) bool {
	if v.Kind() == ValidatorStrongETag && o.Kind() == ValidatorStrongETag {
		return v.ETag == o.ETag
	}
	return v.LastModified != "" && v.LastModified == o.LastModified
}

// Task is the persistent record of one download.
//
// FinalURL is the redacted post-redirect URL, for display only; requests
// always target OriginalURL.
type Task struct {
	ID, OriginalURL, FinalURL, DestDir, FileName, TempPath, FinalPath string

	Size      int64
	SizeKnown bool

	Validators   Validators
	Strategy     Strategy
	DecisionJSON []byte

	State         State
	LastErrorKind ErrorKind
	LastErrorMsg  string

	ExpectedSHA256 string
	ExistingPolicy ExistingFilePolicy
	SettingsJSON   []byte
	VerifyResult   string
	Downloaded     int64

	CreatedAt, UpdatedAt time.Time
}

// SegmentRecord is the persisted progress of one byte range [Start, End).
type SegmentRecord struct {
	Start, End, Pos int64
	Done            bool
	Hash            []byte
	HasherState     []byte
}

// Checkpoint is a snapshot of a task's segment progress.
type Checkpoint struct {
	Segments   []SegmentRecord
	Downloaded int64
}

// Attempt records one failed try and the delay before the next.
type Attempt struct {
	At      time.Time
	Kind    ErrorKind
	Message string
	Delay   time.Duration
}
