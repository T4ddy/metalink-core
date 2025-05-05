package config

import (
	"fmt"
	"os"

	"github.com/spf13/viper"
)

type Config struct {
	Port     string `mapstructure:"PORT"`
	DBUrl    string `mapstructure:"DB_URL"`
	RedisUrl string `mapstructure:"REDIS_URL"`
}

func LoadConfig() (c Config, err error) {
	// Get environment type from ENV variable or use development as default
	env := os.Getenv("APP_ENV")
	if env == "" {
		env = "development"
	}

	// Set default values
	viper.SetDefault("PORT", ":8080")

	// Load environment file
	viper.SetConfigName(fmt.Sprintf(".env.%s", env))
	viper.SetConfigType("env")
	viper.AddConfigPath(".") // Look in the project root directory

	// Environment variables take precedence over config file
	viper.AutomaticEnv()

	// Try to read config file
	if err := viper.ReadInConfig(); err != nil {
		// Continue even if file is not found
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return c, err
		}
	}

	// Map the values to the Config struct
	err = viper.Unmarshal(&c)
	return
}
