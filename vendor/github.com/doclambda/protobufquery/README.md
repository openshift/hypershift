protobufquery
<!-- ==== -->
<!-- [![Build Status](https://travis-ci.org/antchfx/protobufquery.svg?branch=master)](https://travis-ci.org/antchfx/protobufquery)
[![Coverage Status](https://coveralls.io/repos/github/antchfx/protobufquery/badge.svg?branch=master)](https://coveralls.io/github/antchfx/protobufquery?branch=master)
[![GoDoc](https://godoc.org/github.com/antchfx/protobufquery?status.svg)](https://godoc.org/github.com/antchfx/protobufquery)
[![Go Report Card](https://goreportcard.com/badge/github.com/antchfx/protobufquery)](https://goreportcard.com/report/github.com/antchfx/protobufquery) -->

Overview
===

Protobufquery is an XPath query package for ProtocolBuffer documents. It lets you extract data from parsed
ProtocolBuffer message through an XPath expression. Built-in XPath expression cache avoid re-compilation of
XPath expression for each query.

Getting Started
===

### Install Package
```
go get github.com/doclambda/protobufquery
```

#### Load ProtocolBuffer message.

```go
msg := addressbookSample.ProtoReflect()
doc, err := Parse(msg)
```

ProtocolBuffer messages can also be instantiated dynamically using [the dynamicpb package](https://pkg.go.dev/google.golang.org/protobuf/types/dynamicpb). Check out the referenced documentation for examples on loading bytes into those instances.

#### Example data

Using the ProtocolBuffer definition in `testcases/addressbook` we can define an example addressbook as
```go
addressbook.AddressBook{
	People: []*addressbook.Person {
		{
			Name:  "John Doe",
			Id:    101,
			Email: "john@example.com",
			Age:   42,
		},
		{
			Name: "Jane Doe",
			Id:   102,
			Age:  40,
		},
		{
			Name:  "Jack Doe",
			Id:    201,
			Email: "jack@example.com",
			Age:   12,
			Phones: []*addressbook.Person_PhoneNumber{
				{Number: "555-555-5555", Type: addressbook.Person_WORK},
			},
		},
		{
			Name:  "Jack Buck",
			Id:    301,
			Email: "buck@example.com",
			Age:   19,
			Phones: []*addressbook.Person_PhoneNumber{
				{Number: "555-555-0000", Type: addressbook.Person_HOME},
				{Number: "555-555-0001", Type: addressbook.Person_MOBILE},
				{Number: "555-555-0002", Type: addressbook.Person_WORK},
			},
		},
		{
			Name:  "Janet Doe",
			Id:    1001,
			Email: "janet@example.com",
			Age:   16,
			Phones: []*addressbook.Person_PhoneNumber{
				{Number: "555-777-0000"},
				{Number: "555-777-0001", Type: addressbook.Person_HOME},
			},
		},
	},
	Tags: []string {"home", "private", "friends"},
}
```
Using this definition we can perform the example queries below.

#### Get the XML equivalent of the addressbook.
```go
xml := doc.OutputXML()
```


#### Find all names in the addressbook.
```go
list := protobufquery.Find(doc, "/descendant::*[name() = 'people']/name")
// or equal to
list := protobufquery.Find(doc, "//name")
// or by QueryAll()
nodes, err := protobufquery.QueryAll(doc, "//name")
```

#### Find the third entry in the addressbook.
```go
list := protobufquery.Find(doc, "/people[3]")
```

#### Find the first phone number.
```go
book := protobufquery.Find(doc, "//phones[1]/number")
```

#### Find the last phone number.
```go
book := protobufquery.Find(doc, "//phones[last()]/number")
```

#### Find all people without email address.
```go
list := protobufquery.Find(doc, "/people[not(email)]")
```

#### Find all persons older than 18.
```go
list := protobufquery.Find(doc, "/people[age > 18]")
```

Examples
===
```go
func main() {
	addressbookSample := &addressbook.AddressBook{
		People: []*addressbook.Person {
			{
				Name:  "John Doe",
				Id:    101,
				Email: "john@example.com",
				Age:   42,
			},
			{
				Name: "Jane Doe",
				Id:   102,
				Age:  40,
			},
			{
				Name:  "Jack Doe",
				Id:    201,
				Email: "jack@example.com",
				Age:   12,
				Phones: []*addressbook.Person_PhoneNumber{
					{Number: "555-555-5555", Type: addressbook.Person_WORK},
				},
			},
			{
				Name:  "Jack Buck",
				Id:    301,
				Email: "buck@example.com",
				Age:   19,
				Phones: []*addressbook.Person_PhoneNumber{
					{Number: "555-555-0000", Type: addressbook.Person_HOME},
					{Number: "555-555-0001", Type: addressbook.Person_MOBILE},
					{Number: "555-555-0002", Type: addressbook.Person_WORK},
				},
			},
			{
				Name:  "Janet Doe",
				Id:    1001,
				Email: "janet@example.com",
				Age:   16,
				Phones: []*addressbook.Person_PhoneNumber{
					{Number: "555-777-0000"},
					{Number: "555-777-0001", Type: addressbook.Person_HOME},
				},
			},
		},
		Tags: []string {"home", "private", "friends"},
	}
	doc, err := protobufquery.Parse(addressbookSample.ProtoReflect())
	if err != nil {
		panic(err)
	}

	nodes, err := protobufquery.QueryAll(doc, "//people")
	if err != nil {
		panic(err)
	}

	for _, person := range nodes {
		name := protobufquery.FindOne(person, "name").InnerText()
		numbers := make([]string, 0)
		for _, node := range protobufquery.Find(person, "phones/number") {
			numbers = append(numbers, node.InnerText())
		}
		fmt.Printf("%s: %s", name, strings.Join(numbers, ","))
	}
}
```

Implement Principle
===
If you are familiar with XPath and XML, you can easily figure out how to write your XPath expression.

```go
addressbook.AddressBook{
	People: []*addressbook.Person {
		{
			Name:  "John Doe",
			Id:    101,
			Email: "john@example.com",
			Age:   42,
		},
		{
			Name: "Jane Doe",
			Id:   102,
			Age:  40,
		},
		{
			Name:  "Jack Doe",
			Id:    201,
			Email: "jack@example.com",
			Age:   12,
			Phones: []*addressbook.Person_PhoneNumber{
				{Number: "555-555-5555", Type: addressbook.Person_WORK},
			},
		},
		{
			Name:  "Jack Buck",
			Id:    301,
			Email: "buck@example.com",
			Age:   19,
			Phones: []*addressbook.Person_PhoneNumber{
				{Number: "555-555-0000", Type: addressbook.Person_HOME},
				{Number: "555-555-0001", Type: addressbook.Person_MOBILE},
				{Number: "555-555-0002", Type: addressbook.Person_WORK},
			},
		},
		{
			Name:  "Janet Doe",
			Id:    1001,
			Email: "janet@example.com",
			Age:   16,
			Phones: []*addressbook.Person_PhoneNumber{
				{Number: "555-777-0000"},
				{Number: "555-777-0001", Type: addressbook.Person_HOME},
			},
		},
	},
	Tags: []string {"home", "private", "friends"},
}
```

The above ProtocolBuffer representation above will be convert by *protobufquery* to a structure similar to the XML document below:

```XML
<?xml version="1.0" encoding="UTF-8"?>
<people>
  <name>John Doe</name>
  <id>101</id>
  <email>john@example.com</email>
  <age>42</age>
</people>
<people>
  <name>Jane Doe</name>
  <id>102</id>
  <age>40</age>
</people>
<people>
  <name>Jack Doe</name>
  <id>201</id>
  <email>jack@example.com</email>
  <age>12</age>
  <phones>
    <number>555-555-5555</number>
    <type>2</type>
  </phones>
</people>
<people>
  <name>Jack Buck</name>
  <id>301</id>
  <email>buck@example.com</email>
  <age>19</age>
  <phones>
    <number>555-555-0000</number>
    <type>1</type>
  </phones>
  <phones>
    <number>555-555-0001</number>
  </phones>
  <phones>
    <number>555-555-0002</number>
    <type>2</type>
  </phones>
</people>
<people>
  <name>Janet Doe</name>
  <id>1001</id>
  <email>janet@example.com</email>
  <age>16</age>
  <phones>
    <number>555-777-0000</number>
  </phones>
  <phones>
    <number>555-777-0001</number>
    <type>1</type>
  </phones>
</people>
<tags>
  <element>home</element>
  <element>private</element>
  <element>friends</element>
</tags>
```

Note: `element` is an anonymous element without name.

List of XPath query packages
===
|Name |Description |
|--------------------------|----------------|
|[htmlquery](https://github.com/antchfx/htmlquery) | XPath query package for the HTML document|
|[xmlquery](https://github.com/antchfx/xmlquery) | XPath query package for the XML document|
|[jsonquery](https://github.com/antchfx/jsonquery) | XPath query package for the JSON document|
|[protobufquery](https://github.com/doclambda/protobufquery) | XPath query package for ProtocolBuffer messages|
