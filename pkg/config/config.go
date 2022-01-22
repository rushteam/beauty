package config

import "github.com/spf13/viper"

//Config ..
type Config struct {
	*viper.Viper
}

//New ..
func New(name string) (*Config, error) {
	v := viper.New()
	// v.AddConfigPath(basePath + "/config/" + env + "/")
	// v.SetConfigName(name) // 设置配置文件名 (不带后缀)
	v.SetConfigFile(name)
	v.SetConfigType("yaml")
	err := v.ReadInConfig() // 读取配置数据
	if err != nil {
		return nil, err
	}
	return &Config{Viper: v}, nil
}
