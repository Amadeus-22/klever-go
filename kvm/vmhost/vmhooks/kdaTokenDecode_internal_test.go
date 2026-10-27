package vmhooks

import (
	"bytes"
	"fmt"
	"math/rand"
	"testing"

	"github.com/klever-io/klever-go/kapps"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

// kdaTokenDecodedLen must not depend on map iteration order: the same records, built again with
// their map entries inserted in another order, size the same every time, and the size is the
// encoded length (checked once here; the hooks never marshal to size). A nil record counts nothing
func TestKDATokenDecodedLen_Deterministic(t *testing.T) {
	t.Parallel()

	keys := make([]string, 64)
	for i := range keys {
		keys[i] = fmt.Sprintf("entry-%03d-%s", i, bytes.Repeat([]byte{'k'}, i%17))
	}
	buildUser := func(order []int) *kapps.UserKDA {
		user := &kapps.UserKDA{Balance: 5, Metadata: []byte("meta"), Buckets: map[string]*kapps.UserBucket{}}
		for _, i := range order {
			user.Buckets[keys[i]] = &kapps.UserBucket{
				Value:      int64(i) * 1_000_003,
				StakedAt:   int64(i),
				Delegation: bytes.Repeat([]byte{'d'}, i%33),
			}
		}
		return user
	}
	buildKDA := func(order []int) *kapps.KDAData {
		kda := &kapps.KDAData{Name: []byte("name"), URIs: map[string]string{}}
		for _, i := range order {
			kda.URIs[keys[i]] = string(bytes.Repeat([]byte{'u'}, i%29))
		}
		return kda
	}

	random := rand.New(rand.NewSource(2728))
	first := kdaTokenDecodedLen(buildKDA(random.Perm(len(keys))), buildUser(random.Perm(len(keys))))
	for i := 0; i < 50; i++ {
		assert.Equal(t, first, kdaTokenDecodedLen(buildKDA(random.Perm(len(keys))), buildUser(random.Perm(len(keys)))))
	}

	encodedKDA, err := proto.Marshal(buildKDA(random.Perm(len(keys))))
	require.NoError(t, err)
	encodedUser, err := proto.Marshal(buildUser(random.Perm(len(keys))))
	require.NoError(t, err)
	assert.Equal(t, uint64(len(encodedKDA)+len(encodedUser)), first)

	assert.Equal(t, uint64(len(encodedKDA)), kdaTokenDecodedLen(buildKDA(random.Perm(len(keys))), nil))
	assert.Equal(t, uint64(len(encodedUser)), kdaTokenDecodedLen(nil, buildUser(random.Perm(len(keys)))))
	assert.Zero(t, kdaTokenDecodedLen(nil, nil))
	assert.Zero(t, kdaTokenDecodedLen(&kapps.KDAData{}, &kapps.UserKDA{}))

	// literal sizes, so a protobuf or schema change that moves proto.Size breaks here rather than
	// silently repricing every KDA read: the empty KDAData with empty Properties and Attributes
	// messages is 6 bytes, a UserKDA with Balance 1 is 2 more
	stubKDA := func() *kapps.KDAData {
		return &kapps.KDAData{Properties: &kapps.PropertiesData{}, Attributes: &kapps.AttributesData{}}
	}
	assert.Equal(t, uint64(8), kdaTokenDecodedLen(stubKDA(), &kapps.UserKDA{Balance: 1}))
	assert.Equal(t, uint64(6), kdaTokenDecodedLen(stubKDA(), nil))
	// the multi-entry fixture above, computed once from the current kapps proto definitions: a
	// change here means the schema or the protobuf library changed, not a flaky test
	assert.Equal(t, uint64(5417), first)
}
