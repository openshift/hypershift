package client

type Object any
type Patch any

type StatusWriter interface {
	Update(ctx any, obj Object, opts ...any) error
	Patch(ctx any, obj Object, patch Patch, opts ...any) error
}

type Client interface {
	Status() StatusWriter
	Patch(ctx any, obj Object, patch Patch, opts ...any) error
}

func MergeFrom(obj Object) Patch { return nil }

func MergeFromWithOptions(obj Object, opts ...any) Patch { return nil }

type MergeFromWithOptimisticLock struct{}
