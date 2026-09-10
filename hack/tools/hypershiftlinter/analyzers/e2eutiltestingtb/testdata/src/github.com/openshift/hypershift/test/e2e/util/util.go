package util

import "testing"

type AliasTB = testing.TB

type TBFunc func(testing.TB)

func AcceptsTB(testing.TB) {}

func AcceptsAlias(AliasTB) {}

func AcceptsT(*testing.T) {}

var FunctionValue TBFunc

func Pure() {}
