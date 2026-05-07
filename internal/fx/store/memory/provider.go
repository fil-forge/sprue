package memory

import (
	"github.com/fil-forge/sprue/pkg/store/agent"
	memagent "github.com/fil-forge/sprue/pkg/store/agent/memory"
	blobregistry "github.com/fil-forge/sprue/pkg/store/blob_registry"
	memblobregistry "github.com/fil-forge/sprue/pkg/store/blob_registry/memory"
	"github.com/fil-forge/sprue/pkg/store/consumer"
	memconsumer "github.com/fil-forge/sprue/pkg/store/consumer/memory"
	"github.com/fil-forge/sprue/pkg/store/customer"
	memcustomer "github.com/fil-forge/sprue/pkg/store/customer/memory"
	"github.com/fil-forge/sprue/pkg/store/delegation"
	memdelegation "github.com/fil-forge/sprue/pkg/store/delegation/memory"
	"github.com/fil-forge/sprue/pkg/store/metrics"
	memmetrics "github.com/fil-forge/sprue/pkg/store/metrics/memory"
	"github.com/fil-forge/sprue/pkg/store/replica"
	memreplica "github.com/fil-forge/sprue/pkg/store/replica/memory"
	"github.com/fil-forge/sprue/pkg/store/revocation"
	memrevocation "github.com/fil-forge/sprue/pkg/store/revocation/memory"
	spacediff "github.com/fil-forge/sprue/pkg/store/space_diff"
	memspacediff "github.com/fil-forge/sprue/pkg/store/space_diff/memory"
	storageprovider "github.com/fil-forge/sprue/pkg/store/storage_provider"
	memstorageprovider "github.com/fil-forge/sprue/pkg/store/storage_provider/memory"
	"github.com/fil-forge/sprue/pkg/store/subscription"
	memsubscription "github.com/fil-forge/sprue/pkg/store/subscription/memory"
	"github.com/fil-forge/sprue/pkg/store/upload"
	memupload "github.com/fil-forge/sprue/pkg/store/upload/memory"
	"go.uber.org/fx"
)

var Module = fx.Module("memory-store",
	fx.Provide(
		NewAgentStore,
		NewBlobRegistry,
		NewConsumerStore,
		NewCustomerStore,
		NewDelegationStore,
		NewSpaceMetricsStore,
		NewAdminMetricsStore,
		NewReplicaStore,
		NewRevocationStore,
		NewSpaceDiffStore,
		NewStorageProviderStore,
		NewSubscriptionStore,
		NewUploadStore,
	),
)

func NewAgentStore() agent.Store {
	return memagent.New()
}

func NewBlobRegistry(spaceDiffStore spacediff.Store, consumerStore consumer.Store, spaceMetrics metrics.SpaceStore, adminMetrics metrics.Store) blobregistry.Store {
	return memblobregistry.New(spaceDiffStore, consumerStore, spaceMetrics, adminMetrics)
}

func NewConsumerStore() consumer.Store {
	return memconsumer.New()
}

func NewCustomerStore() customer.Store {
	return memcustomer.New()
}

func NewDelegationStore() delegation.Store {
	return memdelegation.New()
}

func NewSpaceMetricsStore() metrics.SpaceStore {
	return memmetrics.NewSpaceStore()
}

func NewAdminMetricsStore() metrics.Store {
	return memmetrics.New()
}

func NewReplicaStore() replica.Store {
	return memreplica.New()
}

func NewRevocationStore() revocation.Store {
	return memrevocation.New()
}

func NewSpaceDiffStore() spacediff.Store {
	return memspacediff.New()
}

func NewStorageProviderStore() storageprovider.Store {
	return memstorageprovider.New()
}

func NewSubscriptionStore() subscription.Store {
	return memsubscription.New()
}

func NewUploadStore() upload.Store {
	return memupload.New()
}
