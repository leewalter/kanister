package blockstorage_test

import (
	"context"
	"os"
	"testing"

	log "github.com/sirupsen/logrus"
	. "gopkg.in/check.v1"

	"github.com/kanisterio/kanister/pkg/blockstorage"
	"github.com/kanisterio/kanister/pkg/blockstorage/awsebs"
	"github.com/kanisterio/kanister/pkg/blockstorage/getter"
	ktags "github.com/kanisterio/kanister/pkg/blockstorage/tags"
)

const (
	clusterRegionAWS = "us-west-2"
	testTagKey       = "kanister.io/testid"
	testTagValue     = "unittest"
)

func Test(t *testing.T) { TestingT(t) }

type BlockStorageProviderSuite struct {
	storageType   blockstorage.Type
	storageRegion string
	provider      blockstorage.Provider
	volumes       []*blockstorage.Volume
	snapshots     []*blockstorage.Snapshot
}

var _ = Suite(&BlockStorageProviderSuite{storageType: blockstorage.TypeEBS, storageRegion: clusterRegionAWS})

func (s *BlockStorageProviderSuite) SetUpSuite(c *C) {
	config := make(map[string]string)
	var err error
	if s.storageType == blockstorage.TypeEBS {
		config[awsebs.ConfigRegion] = s.storageRegion
		accessKey, ok := os.LookupEnv(awsebs.AccessKeyID)
		if !ok {
			c.Skip("The necessary env variable AWS_ACCESS_KEY_ID is not set.")
		}
		secretAccessKey, ok := os.LookupEnv(awsebs.SecretAccessKey)
		if !ok {
			c.Skip("The necessary env variable AWS_SECRET_ACCESS_KEY is not set.")
		}
		config[awsebs.AccessKeyID] = accessKey
		config[awsebs.SecretAccessKey] = secretAccessKey
	}
	s.provider, err = getter.New().Get(s.storageType, config)
	c.Assert(err, IsNil)
}

func (s *BlockStorageProviderSuite) TearDownTest(c *C) {
	for _, snapshot := range s.snapshots {
		c.Assert(s.provider.SnapshotDelete(context.Background(), snapshot), IsNil)
	}
	s.snapshots = nil

	for _, volume := range s.volumes {
		c.Assert(s.provider.VolumeDelete(context.Background(), volume), IsNil)
	}
	s.volumes = nil
}

func (s *BlockStorageProviderSuite) TestCreateVolume(c *C) {
	vol := s.createVolume(c)
	// Check setting tags on the volume
	tags := map[string]string{"testtag": "testtagvalue"}
	err := s.provider.SetTags(context.Background(), vol, tags)
	c.Assert(err, IsNil)
	volUpdated, err := s.provider.VolumeGet(context.Background(), vol.ID, vol.Az)
	c.Assert(err, IsNil)
	// Check previously set tags still exist
	s.checkTagsExist(c, blockstorage.KeyValueToMap(volUpdated.Tags), blockstorage.KeyValueToMap(vol.Tags))
	// Check new tags were set
	s.checkTagsExist(c, blockstorage.KeyValueToMap(volUpdated.Tags), tags)
	// Check std tags
	s.checkStdTagsExist(c, blockstorage.KeyValueToMap(volUpdated.Tags))

	// Test VolumesList
	s.testVolumesList(c)

	err = s.provider.VolumeDelete(context.Background(), volUpdated)
	c.Assert(err, IsNil)
	// We ensure that multiple deletions are handled.
	err = s.provider.VolumeDelete(context.Background(), volUpdated)
	c.Assert(err, IsNil)
	s.volumes = nil
}

func (s *BlockStorageProviderSuite) TestCreateSnapshot(c *C) {
	snapshot := s.createSnapshot(c)
	// Check setting tags on the snapshot
	tags := map[string]string{"testtag": "testtagvalue"}
	err := s.provider.SetTags(context.Background(), snapshot, tags)
	c.Assert(err, IsNil)
	snap, err := s.provider.SnapshotGet(context.Background(), snapshot.ID)
	c.Assert(err, IsNil)
	// Check previously set tags still exist
	s.checkTagsExist(c, blockstorage.KeyValueToMap(snap.Tags), blockstorage.KeyValueToMap(snapshot.Tags))
	// Check new tags were set
	s.checkTagsExist(c, blockstorage.KeyValueToMap(snap.Tags), tags)
	// Check std tags exist
	s.checkStdTagsExist(c, blockstorage.KeyValueToMap(snap.Tags))

	snapshotGet, err := s.provider.SnapshotGet(context.Background(), snapshot.ID)
	c.Assert(err, IsNil)
	c.Assert(snapshotGet.ID, Equals, snapshot.ID)

	// Also test creating a volume from this snapshot
	tags = map[string]string{testTagKey: testTagValue, "kanister.io/testname": c.TestName()}
	vol, err := s.provider.VolumeCreateFromSnapshot(context.Background(), *snapshot, tags)
	c.Assert(err, IsNil)
	s.volumes = append(s.volumes, vol)
	for _, tag := range snapshot.Volume.Tags {
		if _, found := tags[tag.Key]; !found {
			tags[tag.Key] = tag.Value
		}
	}
	// Check tags were merged
	s.checkTagsExist(c, blockstorage.KeyValueToMap(vol.Tags), tags)
	s.checkStdTagsExist(c, blockstorage.KeyValueToMap(vol.Tags))

	err = s.provider.SnapshotDelete(context.Background(), snapshot)
	c.Assert(err, IsNil)
	// We ensure that multiple deletions are handled.
	err = s.provider.SnapshotDelete(context.Background(), snapshot)
	c.Assert(err, IsNil)
	s.snapshots = nil
}

func (s *BlockStorageProviderSuite) TestSnapshotCopy(c *C) {
	c.Skip("Sometimes, snapcopy takes over 10 minutes. go test declares failure if tests are that slow.")

	srcSnapshot := s.createSnapshot(c)
	dstSnapshot := &blockstorage.Snapshot{
		Type:      srcSnapshot.Type,
		Encrypted: false,
		Size:      srcSnapshot.Size,
		Region:    "us-east-1",
		Volume:    nil,
	}
	snap, err := s.provider.SnapshotCopy(context.TODO(), *srcSnapshot, *dstSnapshot)
	c.Assert(err, IsNil)

	log.Infof("Copied snapshot %v to %v", srcSnapshot.ID, snap.ID)

	config := make(map[string]string)
	if s.storageType == blockstorage.TypeEBS {
		config[awsebs.ConfigRegion] = dstSnapshot.Region
		accessKey := os.Getenv(awsebs.AccessKeyID)
		c.Assert(len(accessKey) > 0, Equals, true)
		secretAccessKey := os.Getenv(awsebs.SecretAccessKey)
		c.Assert(len(secretAccessKey) > 0, Equals, true)
		config[awsebs.AccessKeyID] = accessKey
		config[awsebs.SecretAccessKey] = secretAccessKey
	}
	provider, err := getter.New().Get(s.storageType, config)
	c.Assert(err, IsNil)

	snapDetails, err := provider.SnapshotGet(context.TODO(), snap.ID)
	c.Assert(err, IsNil)

	c.Check(snapDetails.Region, Equals, dstSnapshot.Region)
	c.Check(snapDetails.Size, Equals, srcSnapshot.Size)

	err = provider.SnapshotDelete(context.TODO(), snap)
	c.Assert(err, IsNil)
}

func (s *BlockStorageProviderSuite) testVolumesList(c *C) {
	var tags map[string]string
	if s.provider.Type() == blockstorage.TypeGPD {
		tags = map[string]string{"name": "*"}
	} else {
		tags = map[string]string{"status": "available"}
	}
	vols, err := s.provider.VolumesList(context.Background(), tags)
	c.Assert(err, IsNil)
	c.Assert(vols, NotNil)
	c.Assert(vols, FitsTypeOf, []*blockstorage.Volume{})
	c.Assert(vols, Not(HasLen), 0)
	c.Assert(vols[0].Type, Equals, s.provider.Type())
}

func (s *BlockStorageProviderSuite) TestSnapshotsList(c *C) {
	var tags map[string]string
	testSnaphot := s.createSnapshot(c)
	if s.provider.Type() != blockstorage.TypeEBS {
		tags = map[string]string{"labels." + ktags.SanitizeValueForGCP(testTagKey): testTagValue}
	} else {
		tags = map[string]string{"tag-key": testTagKey, "tag-value": testTagValue}
	}
	snaps, err := s.provider.SnapshotsList(context.Background(), tags)
	c.Assert(err, IsNil)
	c.Assert(snaps, NotNil)
	c.Assert(snaps, FitsTypeOf, []*blockstorage.Snapshot{})
	c.Assert(snaps, Not(HasLen), 0)
	c.Assert(snaps[0].Type, Equals, s.provider.Type())
	s.provider.SnapshotDelete(context.Background(), testSnaphot)
}

// Helpers
func (s *BlockStorageProviderSuite) createVolume(c *C) *blockstorage.Volume {
	tags := []*blockstorage.KeyValue{
		{Key: testTagKey, Value: testTagValue},
		{Key: "kanister.io/testname", Value: c.TestName()},
	}
	vol := blockstorage.Volume{
		Size: 1,
		Tags: tags,
	}
	switch s.storageType {
	case blockstorage.TypeGPD:
		vol.Az = "us-west1-b"
	case blockstorage.TypeEBS:
		vol.Az = "us-west-2b"
	case blockstorage.TypeAD:
		vol.Az = "centralus"
	}

	ret, err := s.provider.VolumeCreate(context.Background(), vol)
	c.Assert(err, IsNil)
	s.volumes = append(s.volumes, ret)
	c.Assert(ret.Size, Equals, int64(1))
	s.checkTagsExist(c, blockstorage.KeyValueToMap(ret.Tags), blockstorage.KeyValueToMap(tags))
	s.checkStdTagsExist(c, blockstorage.KeyValueToMap(ret.Tags))
	return ret
}

func (s *BlockStorageProviderSuite) createSnapshot(c *C) *blockstorage.Snapshot {
	vol := s.createVolume(c)
	tags := map[string]string{testTagKey: testTagValue, "kanister.io/testname": c.TestName()}
	ret, err := s.provider.SnapshotCreate(context.Background(), *vol, tags)
	c.Assert(err, IsNil)
	s.snapshots = append(s.snapshots, ret)
	s.checkTagsExist(c, blockstorage.KeyValueToMap(ret.Tags), tags)
	return ret
}

func (s *BlockStorageProviderSuite) checkTagsExist(c *C, actual map[string]string, expected map[string]string) {
	if s.provider.Type() != blockstorage.TypeEBS {
		expected = blockstorage.SanitizeTags(expected)
	}

	for k, v := range expected {
		c.Check(actual[k], Equals, v)

	}
}

func (s *BlockStorageProviderSuite) checkStdTagsExist(c *C, actual map[string]string) {
	stdTags := ktags.GetStdTags()
	for k := range stdTags {
		c.Check(actual[k], NotNil)
	}
}
