package config

import "github.com/spf13/viper"

var basePath = "."

//Config ..
type Config struct {
	*viper.Viper
}

//SetBasePath set a config default path
func SetBasePath(path string) {
	basePath = path
}

//New ..
func New(env, name string) (*Config, error) {
	v := viper.New()
	v.AddConfigPath(basePath + "/config/" + env + "/")
	v.SetConfigName(name) // 设置配置文件名 (不带后缀)
	v.SetConfigType("yaml")
	err := v.ReadInConfig() // 读取配置数据
	if err != nil {
		return nil, err
	}
	c := &Config{Viper: v}
	return c, nil
}
