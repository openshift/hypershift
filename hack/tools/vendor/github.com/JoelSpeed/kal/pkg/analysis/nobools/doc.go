/*
nobools is an analyzer that checks for usage of bool types.

Boolean values can only ever have 2 states, true or false.
Over time, needs may change, and with a bool type, there is no way to add additional states.
This problem then leads to pairs of bools, where values of one are only valid given the value of another.
This is confusing and error-prone.

It is recommended instead to use a string type with a set of constants to represent the different states,
creating an enum.

By using an enum, not only can you provide meaningul names for the various states of the API,
but you can also add additional states in the future without breaking the API.
*/
package nobools
