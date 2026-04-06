package server

import (
	"net/http"
	"sort"
	"strings"

	"github.com/versionlens/OpenNalvin/internal/config"
)

type openAIModelObject struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

type openAIModelList struct {
	Object string              `json:"object"`
	Data   []openAIModelObject `json:"data"`
}

func (s *Server) handleListModels(w http.ResponseWriter, r *http.Request) {
	models := configuredOpenAIModelObjects()
	s.writeJSON(w, r, http.StatusOK, openAIModelList{
		Object: "list",
		Data:   models,
	})
}

func configuredOpenAIModelObjects() []openAIModelObject {
	providers := config.ListProviders()
	names := make([]string, 0, len(providers))
	for name, provider := range providers {
		if strings.TrimSpace(provider.Model) == "" {
			continue
		}
		if !providerHasUserConfiguration(provider) {
			continue
		}
		names = append(names, name)
	}

	sort.Slice(names, func(i, j int) bool {
		leftDefault := names[i] == "default"
		rightDefault := names[j] == "default"
		if leftDefault != rightDefault {
			return leftDefault
		}
		return names[i] < names[j]
	})

	models := make([]openAIModelObject, 0, len(names))
	for _, name := range names {
		provider := providers[name]
		models = append(models, openAIModelObject{
			ID:      strings.TrimSpace(provider.Model),
			Object:  "model",
			Created: 0,
			OwnedBy: name,
		})
	}
	return models
}

func providerHasUserConfiguration(provider config.ProviderConfig) bool {
	return provider.BaseURL != "" || provider.APIKey != "" || provider.Model != ""
}
