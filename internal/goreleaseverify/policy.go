package goreleaseverify

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

const maxPolicyInputBytes = 512

// CheckPolicy validates only the supplied release identity against the
// verifier's compiled closed policy. It performs no filesystem, Git, artifact,
// module, or network operation and is intended for pre-tag authorization
// comparison. Full release verification remains mandatory after tagging.
func CheckPolicy(request PolicyRequest) (PolicyAuthorization, error) {
	if err := validatePolicyRequest(request); err != nil {
		return PolicyAuthorization{}, err
	}
	switch request.Profile {
	case ProfileGoModule:
		policy, err := selectModulePolicy(request)
		if err != nil {
			return PolicyAuthorization{}, err
		}
		return modulePolicyAuthorization(policy), nil
	case ProfileDistribution:
		policy, err := selectBinaryDistributionPolicy(request)
		if err != nil {
			return PolicyAuthorization{}, err
		}
		return distributionPolicyAuthorization(policy), nil
	default:
		return PolicyAuthorization{}, errors.New("release profile is not independently authorized")
	}
}

func policyRequestFromConfig(config Config) PolicyRequest {
	return PolicyRequest{
		Repository: config.RepositoryName,
		Source:     config.CanonicalSource,
		Module:     config.Module,
		Version:    config.Version,
		Profile:    config.Profile,
	}
}

func validatePolicyRequest(request PolicyRequest) error {
	values := []string{request.Repository, request.Source, request.Module, request.Version, request.Profile}
	for _, value := range values {
		if value == "" || len(value) > maxPolicyInputBytes || !utf8.ValidString(value) ||
			strings.IndexFunc(value, unicode.IsControl) >= 0 {
			return errors.New("release policy identity is missing or invalid")
		}
	}
	return nil
}

func selectModulePolicy(request PolicyRequest) (releasePolicy, error) {
	if strings.HasPrefix(request.Repository, "starter-") {
		return releasePolicy{}, errors.New("starter repositories must use the key-backed library release verifier")
	}
	policy, found := releasePolicies[request.Repository]
	if !found {
		return releasePolicy{}, fmt.Errorf(
			"repository %q is not independently authorized for %s",
			request.Repository,
			ProfileGoModule,
		)
	}
	if request.Module != policy.module || request.Source != policy.source || request.Version != policy.version {
		return releasePolicy{}, errors.New("trusted release inputs do not match independent module policy")
	}
	return policy, nil
}

func selectBinaryDistributionPolicy(request PolicyRequest) (distributionPolicy, error) {
	policy, found := distributionPolicies[request.Repository]
	if !found {
		return distributionPolicy{}, fmt.Errorf(
			"repository %q is not independently authorized for %s",
			request.Repository,
			ProfileDistribution,
		)
	}
	if request.Module != policy.module || request.Source != policy.source || request.Version != policy.version {
		return distributionPolicy{}, errors.New("trusted release inputs do not match independent distribution policy")
	}
	return policy, nil
}

func modulePolicyAuthorization(policy releasePolicy) PolicyAuthorization {
	return PolicyAuthorization{
		Repository: policy.repository,
		Source:     policy.source,
		Module:     policy.module,
		Version:    policy.version,
		Profile:    ProfileGoModule,
	}
}

func distributionPolicyAuthorization(policy distributionPolicy) PolicyAuthorization {
	return PolicyAuthorization{
		Repository: policy.repository,
		Source:     policy.source,
		Module:     policy.module,
		Version:    policy.version,
		Profile:    ProfileDistribution,
	}
}
