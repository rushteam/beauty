package conf_test

// func TestNew(t *testing.T) {
// 	l, err := conf.New("t.yaml")
// 	if err != nil {
// 		t.Error(err)
// 		return
// 	}
// 	c := struct {
// 		App string `mapstructure:"app"`
// 	}{}
// 	if err := l.Unmarshal(&c); err != nil {
// 		t.Error("Unmarshal", err)
// 		return
// 	}
// 	l.Watch(context.TODO(), func() {
// 		l.Unmarshal(&c)
// 	})

// 	if c.App != "test" {
// 		t.Error("error ")
// 	}
// }
