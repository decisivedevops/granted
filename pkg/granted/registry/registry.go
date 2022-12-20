package registry

import (
	"fmt"
	"os"
	"path"
	"sort"
	"strings"

	"github.com/AlecAivazis/survey/v2"
	"github.com/common-fate/clio"
	grantedConfig "github.com/common-fate/granted/pkg/config"
	"gopkg.in/yaml.v3"
)

type Registry struct {
	Config         grantedConfig.Registry
	AwsConfigPaths []string                         `yaml:"awsConfig"`
	TemplateValues []map[string][]map[string]string `yaml:"templateValues"`
}

// GetRegistryLocation returns the directory path where cloned repo is located.
func getRegistryLocation(r grantedConfig.Registry) (string, error) {
	gConfigPath, err := grantedConfig.GrantedConfigFolder()
	if err != nil {
		return "", err
	}

	return path.Join(gConfigPath, "registries", r.Name), nil
}

func (r *Registry) Parse() error {
	const defaultConfigFilename string = "granted.yml"

	filepath, err := getRegistryLocation(r.Config)
	if err != nil {
		return err
	}

	if r.Config.Path != nil {
		filepath = path.Join(filepath, *r.Config.Path)
	}

	if r.Config.Filename != nil {
		filepath = path.Join(filepath, *r.Config.Filename)
	} else {
		filepath = path.Join(filepath, defaultConfigFilename)
	}

	clio.Debugf("verifying if valid config exists in %s", filepath)
	file, err := os.ReadFile(filepath)
	if err != nil {
		return err
	}

	err = yaml.Unmarshal(file, r)
	if err != nil {
		return err
	}

	return nil
}

type registryOptions struct {
	name                    string
	path                    string
	configFileName          string
	ref                     string
	url                     string
	priority                int
	prefixAllProfiles       bool
	prefixDuplicateProfiles bool
}

func NewProfileRegistry(rOpts registryOptions) Registry {
	newRegistry := Registry{
		Config: grantedConfig.Registry{
			Name:                    rOpts.name,
			URL:                     rOpts.url,
			PrefixAllProfiles:       rOpts.prefixAllProfiles,
			PrefixDuplicateProfiles: rOpts.prefixDuplicateProfiles,
		},
	}

	if rOpts.path != "" {
		newRegistry.Config.Path = &rOpts.path
	}

	if rOpts.configFileName != "" {
		newRegistry.Config.Filename = &rOpts.configFileName
	}

	if rOpts.ref != "" {
		newRegistry.Config.Ref = &rOpts.ref
	}

	if rOpts.priority != 0 {
		newRegistry.Config.Priority = &rOpts.priority
	}

	return newRegistry
}

func GetProfileRegistries() ([]Registry, error) {
	gConf, err := grantedConfig.Load()
	if err != nil {
		return nil, err
	}

	if len(gConf.ProfileRegistry.Registries) == 0 {
		return []Registry{}, nil
	}

	var registries []Registry
	for _, r := range gConf.ProfileRegistry.Registries {
		registries = append(registries, Registry{
			Config: r,
		})
	}

	// this will sort the registry based on priority.
	sort.Slice(registries, func(i, j int) bool {
		var a, b int = 0, 0
		if registries[i].Config.Priority != nil {
			a = *registries[i].Config.Priority
		}

		if registries[j].Config.Priority != nil {
			b = *registries[j].Config.Priority
		}

		return a > b
	})

	return registries, nil
}

// Get the key=value from the provided required variable flag.
func formatKey(s string) (string, string, error) {
	value := strings.Split(s, "=")
	if len(value) == 2 {
		return value[0], value[1], nil
	}

	return "", "", fmt.Errorf("invalid value '%s'provided for the required key", s)
}

// granted.yml config might contain user specific variables
// in such case we would prompt users to add them before registry is added.
func (r Registry) PromptRequiredKeys(passedKeys []string) error {
	var questions []*survey.Question

	var requiredKeysThroughFlags = make(map[string]string)

	gConf, err := grantedConfig.Load()
	if err != nil {
		return err
	}

	for _, v := range r.TemplateValues {
		for fieldName, val := range v {
			for _, valMap := range val {

				if _, ok := valMap["isRequired"]; ok {
					if len(passedKeys) != 0 {
						for _, val := range passedKeys {
							key, value, err := formatKey(val)
							if err != nil {
								return err
							}

							requiredKeysThroughFlags[key] = value
						}
					}

					// if the key was passed through cli then skip the prompt
					if _, ok := requiredKeysThroughFlags[fieldName]; ok {
						err := SaveKey(gConf, fieldName, requiredKeysThroughFlags[fieldName])
						if err != nil {
							return err
						}

						break
					}

					// if the key is already configured then skip
					if gConf.ProfileRegistry.RequiredKeys[fieldName] != "" {
						clio.Debugf("%s is already configured so skipping", fieldName)

						break
					}

					qs := survey.Question{
						Name:     fieldName,
						Prompt:   &survey.Input{Message: fmt.Sprintf("'%s': %s", fieldName, valMap["prompt"])},
						Validate: survey.Required}

					questions = append(questions, &qs)
					ansmap := make(map[string]interface{})

					if len(questions) > 0 {
						clio.Info("Your Profile Registry requires you to input values for the following keys:")

						err = survey.Ask(questions, &ansmap)
						if err != nil {
							return err
						}

						err = SaveKeys(gConf, ansmap)
						if err != nil {
							return err
						}

						break
					}

				} else {
					// for all other variables add them to registry as variables
					if val, ok := valMap["value"]; ok {

						if gConf.ProfileRegistry.Variables == nil {
							gConf.ProfileRegistry.Variables = make(map[string]string)
							gConf.ProfileRegistry.Variables[fieldName] = val
						} else {
							gConf.ProfileRegistry.Variables[fieldName] = val
						}
						err := gConf.Save()
						if err != nil {
							return err
						}
					} else {
						clio.Warnf("variable '%s' doesnot have associated 'value' key with it.", fieldName)
					}

					continue
				}
			}
		}
	}

	return nil
}

// This is used when user enters the required key through cli prompts.
func SaveKeys(gConf *grantedConfig.Config, ansmap map[string]interface{}) error {
	for k, v := range ansmap {
		if len(gConf.ProfileRegistry.RequiredKeys) == 0 {
			var requiredKeys = make(map[string]string)
			requiredKeys[k] = v.(string)
			gConf.ProfileRegistry.RequiredKeys = requiredKeys
		} else {
			gConf.ProfileRegistry.RequiredKeys[k] = v.(string)
		}
	}

	err := gConf.Save()
	if err != nil {
		return err
	}

	return nil
}

// This is used when user passed the required value through flag.
func SaveKey(gConf *grantedConfig.Config, key string, value string) error {
	if len(gConf.ProfileRegistry.RequiredKeys) == 0 {
		var requiredKeys = make(map[string]string)
		requiredKeys[key] = value
		gConf.ProfileRegistry.RequiredKeys = requiredKeys
	} else {
		gConf.ProfileRegistry.RequiredKeys[key] = value
	}

	err := gConf.Save()
	if err != nil {
		return err
	}

	return nil
}
