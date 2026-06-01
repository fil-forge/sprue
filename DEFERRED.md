# Items deferred from first round of RFC 7 work:

* Separation of DID resolver and verifier resolver
  * Should become two separate handler systems: one by DID method, the other by verification method type.
  * Make an interface so we can replace `map[string]validator.DIDVerifierResolverFunc` with a map type that has a `Resolve()` method.
* Delegation payloads as requests
  * Should change `RequestArguments` to take an entire UCAN delegation payload rather than some of the pieces to construct from.
* Invocation command
  * `/ucan/attest/proof` doesn't make sense when it's not necessarily a proof. Also, the args shouldn't point to a "proof".
* Recursive DID resolution
  * DIDMailtoResolver should be able to resolve any DID to a verifier, not just take the one verifier.
* "github.com/fil-forge/sprue/pkg/identity" shouldn't exist: it's bridging between `did:key` and other DIDs in an inappropriate way.
* Should the "token store" become just a "delegation store"?