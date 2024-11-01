package cron

import (
	"context"
	"reflect"
	"testing"
)

func TestWithCronHandler(t *testing.T) {
	type args struct {
		spec    string
		handler func(ctx context.Context) error
	}
	tests := []struct {
		name string
		args args
		want CronOptions
	}{
		{
			name: "",
			args: args{
				spec: "@every 5m",
				handler: func(ctx context.Context) error {
					return nil
				},
			},
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := WithCronHandler(tt.args.spec, tt.args.handler)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("WithCronHandler() = %v, want %v", got, tt.want)
			}
			got(New())
		})
	}
}
