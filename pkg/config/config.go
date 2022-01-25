package config

import "github.com/spf13/viper"

//Config ..
type Config struct {
	*viper.Viper
}

//Sub 子配置 warp viper.Viper
func (c *Config) Sub(key string) *Config {
	if conf := c.Viper.Sub(key); conf != nil {
		return &Config{
			Viper: conf,
		}
	}
	return nil
}

//New ..
func New(filename string) (*Config, error) {
	v := viper.New()
	// v.AddConfigPath(basePath + "/config/" + env + "/")
	// v.SetConfigName(name) // 设置配置文件名 (不带后缀)
	// v.SetConfigType("yaml")
	v.SetConfigFile(filename)
	err := v.ReadInConfig() // 读取配置数据
	if err != nil {
		return nil, err
	}
	return &Config{Viper: v}, nil
}
