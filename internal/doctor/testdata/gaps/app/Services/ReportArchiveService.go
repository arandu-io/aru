package services

import (
	"context"
	"io"

	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/hesape/filesystem"

	models "example.test/gaps/app/Models"
	policies "example.test/gaps/app/Policies"
)

// ReportArchiveService hands out the exported file of a report, from the
// tenant's disk.
//
// Its reads have the shape resource-not-reauthorized looks for -- Authorize,
// then a Get given a context, the Grant and a key -- and they are not reads of
// a row. Disk.Get opens a file under the prefix the Grant decides, so there is
// no entity to hand a second Authorize, and the rule was reporting the method
// for a decision that has no object to be about. What tells it apart is the
// type the call is made on, which the method name alone cannot say.
type ReportArchiveService struct {
	disk   *filesystem.Disk
	policy policies.ReportPolicy
}

// Download reads the file through the disk the service holds.
func (s *ReportArchiveService) Download(ctx context.Context, actor security.Subject, key string) ([]byte, error) {
	g, err := security.Authorize(ctx, s.policy, actor, policies.ActionViewReport, models.Report{})
	if err != nil {
		return nil, err
	}

	file, err := s.disk.Get(ctx, g, key)
	if err != nil {
		return nil, err
	}
	defer file.Body.Close()
	return io.ReadAll(file.Body)
}

// DownloadFrom is the same read through a disk the caller passes in.
func (s *ReportArchiveService) DownloadFrom(ctx context.Context, disk *filesystem.Disk, actor security.Subject, key string) ([]byte, error) {
	g, err := security.Authorize(ctx, s.policy, actor, policies.ActionViewReport, models.Report{})
	if err != nil {
		return nil, err
	}

	file, err := disk.Get(ctx, g, key)
	if err != nil {
		return nil, err
	}
	defer file.Body.Close()
	return io.ReadAll(file.Body)
}
