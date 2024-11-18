package cron

import (
	"context"
	"fmt"
	"testing"
)

// 普通函数
func NormalFunction(ctx context.Context) error {
	fmt.Println("This is an example function")
	return nil
}

// 定义结构体和方法
type MyStruct struct {
	Name string
}

func (m MyStruct) MyMethod(ctx context.Context) error {
	fmt.Printf("This is MyMethod of %s\n", m.Name)
	return nil
}

func (m *MyStruct) PointerMethod(ctx context.Context) error {
	fmt.Printf("This is PointerMethod of %s\n", m.Name)
	return nil
}

func TestWithCronHandler(t *testing.T) {
	type args struct {
		spec    string
		handler func(ctx context.Context) error
	}
	tests := []struct {
		name string
		args args
		//want CronOptions
	}{
		{
			name: "",
			args: args{
				spec:    "@every 5m",
				handler: NormalFunction,
			},
			//want: nil,
		},
		{
			name: "",
			args: args{
				spec:    "@every 5m",
				handler: (&MyStruct{Name: "MyStruct"}).MyMethod,
			},
			//want: nil,
		},
		{
			name: "",
			args: args{
				spec:    "@every 5m",
				handler: (&MyStruct{Name: "MyStruct"}).PointerMethod,
			},
			//want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := WithCronHandler(tt.args.spec, tt.args.handler)
			//if !reflect.DeepEqual(got, tt.want) {
			//	t.Errorf("WithCronHandler() = %v, want %v", got, tt.want)
			//}
			got(New())
		})
	}
}
