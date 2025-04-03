/*
integers is an analyzer that checks for usage of unsupported integer types.

According to the API conventions, only int32 and int64 types should be used in Kubernetes APIs.

int32 is preferred and should be used in most cases, unless the use case requireds representing
values larger than int32.

It also states that unsigned integers should be replaced with signed integers, and then numeric
lower bounds added to prevent negative integers.

Succinctly this anaylzer checks for int, int8, int16, uint, uint8, uint16, uint32 and uint64 types
and highlights that they should not be used.
*/
package integers
