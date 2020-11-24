package pgd

import (
	"bytes"
	"fmt"
	"github.com/dave/jennifer/jen"
	"github.com/davecgh/go-spew/spew"
	"github.com/lyft/protoc-gen-star"
	pgsgo "github.com/lyft/protoc-gen-star/lang/go"
	dynamopb "github.com/pquerna/protoc-gen-dynamo/dynamo"
)

const (
	moduleName    = "dynamo"
	version       = "0.1.0"
	commentFormat = `// Code generated by protoc-gen-%s v%s. DO NOT EDIT.
// source: %s
`
)

type Module struct {
	*pgs.ModuleBase
	ctx pgsgo.Context
}

var _ pgs.Module = (*Module)(nil)

func New() pgs.Module {
	return &Module{ModuleBase: &pgs.ModuleBase{}}
}

func (m *Module) InitContext(ctx pgs.BuildContext) {
	m.ModuleBase.InitContext(ctx)
	m.ctx = pgsgo.InitContext(ctx.Parameters())
}

func (m *Module) Name() string {
	return moduleName
}

func (m *Module) Execute(targets map[string]pgs.File, pkgs map[string]pgs.Package) []pgs.Artifact {
	for _, f := range targets {
		msgs := f.AllMessages()
		if n := len(msgs); n == 0 {
			m.Debugf("No messagess in %v, skipping", f.Name())
			continue
		}
		m.processFile(f)
	}
	return m.Artifacts()
}

func (m *Module) processFile(f pgs.File) {
	out := bytes.Buffer{}
	err := m.applyTemplate(&out, f)
	if err != nil {
		m.Logf("couldn't apply template: %s", err)
		m.Fail("code generation failed")
	} else {
		generatedFileName := m.ctx.OutputPath(f).SetExt(fmt.Sprintf(".%s.go", moduleName)).String()
		m.AddGeneratorFile(generatedFileName, out.String())
	}
}

const (
	dynamoPkg  = "github.com/aws/aws-sdk-go/service/dynamodb"
	protoPkg   = "github.com/golang/protobuf/proto"
	awsPkg     = "github.com/aws/aws-sdk-go/aws"
	strconvPkg = "strconv"
	stringsPkg = "strings"
	fmtPkg     = "fmt"
)

func (m *Module) applyTemplate(buf *bytes.Buffer, in pgs.File) error {
	pkgName := m.ctx.PackageName(in).String()
	importPath := m.ctx.ImportPath(in).String()
	protoFileName := in.Name().String()

	f := jen.NewFilePathName(importPath, pkgName)
	f.HeaderComment(fmt.Sprintf(commentFormat, moduleName, version, protoFileName))

	f.ImportName(dynamoPkg, "dynamodb")
	f.ImportName(awsPkg, "aws")
	f.ImportName(protoPkg, "proto")
	f.ImportName(strconvPkg, "strconv")
	f.ImportName(fmtPkg, "fmt")
	f.ImportName(stringsPkg, "strings")

	// https://godoc.org/github.com/aws/aws-sdk-go/service/dynamodb/dynamodbattribute#Marshaler
	// https://godoc.org/github.com/guregu/dynamo#Marshaler
	err := m.applyMarshal(f, in)
	if err != nil {
		return err
	}

	// https://godoc.org/github.com/guregu/dynamo#Unmarshaler
	// UnmarshalDynamo(av *dynamodb.AttributeValue) error
	err = m.applyUnmarshal(f, in)
	if err != nil {
		return err
	}

	return f.Render(buf)
}

type avType string

const (
	avt_bytes      avType = "B"
	avt_bool       avType = "BOOL"
	avt_byte_set   avType = "BS"
	avt_list       avType = "L"
	avt_map        avType = "M"
	avt_number     avType = "N"
	avt_number_set avType = "NS"
	avt_null       avType = "NULL"
	avt_string     avType = "S"
	avt_string_set avType = "SS"
)

func getAVType(field pgs.Field, fext *dynamopb.DynamoFieldOptions) avType {
	isArray := field.Type().ProtoLabel() == pgs.Repeated
	pt := field.Type().ProtoType()

	if isArray {
		if !fext.Type.Set {
			return avt_list
		}
		switch {
		case pt.IsInt() || pt == pgs.DoubleT || pt == pgs.FloatT:
			return avt_number_set
		case pt == pgs.StringT:
			return avt_string_set
		case pt == pgs.BytesT:
			return avt_byte_set
		case pt == pgs.EnumT:
			return avt_number_set
		}
	} else {
		switch {
		case pt.IsInt() || pt == pgs.DoubleT || pt == pgs.FloatT:
			return avt_number
		case pt == pgs.BoolT:
			return avt_bool
		case pt == pgs.StringT:
			return avt_string
		case pt == pgs.BytesT:
			return avt_bytes
		case pt == pgs.MessageT:
			return avt_map
		case pt == pgs.EnumT:
			return avt_number
		}
	}
	panic(fmt.Sprintf("getAVType: failed to determine dynamodb type: %T %+v", field, fext.Type))
}

func fieldByName(msg pgs.Message, name string) pgs.Field {
	for _, f := range msg.Fields() {
		if f.Name().LowerSnakeCase().String() == name {
			return f
		}
	}
	panic(fmt.Sprintf("Failed to find field %s on %s", name, msg.FullyQualifiedName()))
}

const (
	valueField = "value"
	typeField  = "typ"
)

func (m *Module) applyMarshal(f *jen.File, in pgs.File) error {
	for _, msg := range in.AllMessages() {
		structName := m.ctx.Name(msg)
		mext := dynamopb.DynamoMessageOptions{}
		ok, err := msg.Extension(dynamopb.E_Msg, &mext)
		if err != nil {
			m.Logf("Parsing dynamo.msg.disabled failed: %s", err)
			m.Fail("code generation failed")
		}
		if ok && mext.Disabled {
			m.Logf("dynamo.msg disabled for %s", structName)
			continue
		}
		// https://godoc.org/github.com/guregu/dynamo#Marshaler:
		// MarshalDynamo() (*dynamodb.AttributeValue, error)
		f.Func().Params(
			jen.Id("p").Op("*").Id(structName.String()),
		).Id("MarshalDynamo").Params().List(jen.Params(jen.Op("*").Qual(dynamoPkg, "AttributeValue"), jen.Id("error"))).Block(
			jen.Id("av").Op(":=").Op("&").Qual(dynamoPkg, "AttributeValue").Values(),
			jen.Id("err").Op(":=").Id("p").Dot("MarshalDynamoDBAttributeValue").Call(jen.Id("av")),
			jen.If(jen.Id("err").Op("!=").Nil()).Block(
				jen.Return(jen.Nil(), jen.Id("err")),
			),
			jen.Return(jen.Id("av"), jen.Nil()),
		).Line()

		f.Func().Params(
			jen.Id("p").Op("*").Id(structName.String()),
		).Id("MarshalDynamoItem").Params().List(jen.Params(jen.Map(jen.String()).Op("*").Qual(dynamoPkg, "AttributeValue"), jen.Id("error"))).Block(
			jen.Id("av").Op(":=").Op("&").Qual(dynamoPkg, "AttributeValue").Values(),
			jen.Id("err").Op(":=").Id("p").Dot("MarshalDynamoDBAttributeValue").Call(jen.Id("av")),
			jen.If(jen.Id("err").Op("!=").Nil()).Block(
				jen.Return(jen.Nil(), jen.Id("err")),
			),
			jen.Return(jen.Id("av").Dot("M"), jen.Nil()),
		).Line()

		stmts := []jen.Code{}
		refId := 0
		d := jen.Dict{}
		needErr := false
		needNullBoolTrue := false
		needProtoBuffer := false
		needStringBuilder := false
		const protoBuffer = "pbuf"
		const stringBuffer = "sb"
		computedKeys := make([]*dynamopb.Key, 0)
		if mext.Partition != nil {
			computedKeys = append(computedKeys, mext.Partition)
		}
		if mext.Sort != nil {
			computedKeys = append(computedKeys, mext.Sort)
		}
		if mext.CompoundField != nil {
			computedKeys = append(computedKeys, mext.CompoundField...)
		}

		if false {
			m.Log(spew.Sprint(computedKeys))
		}

		for _, ck := range computedKeys {
			refId++
			vname := fmt.Sprintf("v%d", refId)

			sep := ck.Separator
			if sep == "" {
				sep = ":"
			}

			needStringBuilder = true
			stmts = append(stmts, jen.Id(vname).Op(":=").Op("&").Qual(dynamoPkg, "AttributeValue").Values())
			stmts = append(stmts, jen.Id(stringBuffer).Dot("Reset").Call())

			if ck.Prefix != "" {
				stmts = append(stmts, jen.List(jen.Id("_"), jen.Id("_")).Op("=").Id(stringBuffer).Dot("WriteString").Call(
					jen.Lit(ck.Prefix+sep),
				))
			}

			first := true
			for _, fn := range ck.Fields {
				field := fieldByName(msg, fn)
				pt := field.Type().ProtoType()
				srcName := field.Name().UpperCamelCase().String()
				if !first {
					stmts = append(stmts, jen.List(jen.Id("_"), jen.Id("_")).Op("=").Id(stringBuffer).Dot("WriteString").Call(
						jen.Lit(sep),
					))
				}
				first = false
				switch {
				case pt == pgs.StringT:
					stmts = append(stmts, jen.List(jen.Id("_"), jen.Id("_")).Op("=").Id(stringBuffer).Dot("WriteString").Call(
						jen.Id("p").Dot(srcName),
					))
				case pt.IsNumeric():
					fmtCall := numberFormatStatement(pt, jen.Id("p").Dot(srcName))
					stmts = append(stmts, jen.List(jen.Id("_"), jen.Id("_")).Op("=").Id(stringBuffer).Dot("WriteString").Call(
						fmtCall,
					))
				default:
					panic(fmt.Sprintf("Compound key: unsupported type: %s", pt.String()))
				}
			}
			stmts = append(stmts, jen.Id(vname).Dot("S").Op("=").Qual(awsPkg, "String").Call(jen.Id(stringBuffer).Dot("String").Call()))
			d[jen.Lit(ck.Name)] = jen.Id(vname)
		}

		typeName := fmt.Sprintf("type.googleapis.com/%s.%s", msg.Package().ProtoName().String(), msg.Name())

		needProtoBuffer = true
		needErr = true
		refId++
		vname := fmt.Sprintf("v%d", refId)
		stmts = append(stmts, jen.Id(vname).Op(":=").Op("&").Qual(dynamoPkg, "AttributeValue").Values())
		stmts = append(stmts, jen.Id(protoBuffer).Dot("Reset").Call())
		stmts = append(stmts, jen.Id("err").Op("=").Id(protoBuffer).Dot("Marshal").Call(jen.Id("p")))
		stmts = append(stmts,
			jen.If(jen.Id("err").Op("!=").Nil()).Block(
				jen.Return(jen.Id("err")),
			),
		)
		stmts = append(stmts, jen.Id(vname).Dot("B").Op("=").Id(protoBuffer).Dot("Bytes").Call())
		d[jen.Lit(valueField)] = jen.Id(vname)

		refId++
		vname = fmt.Sprintf("v%d", refId)
		stmts = append(stmts, jen.Id(vname).Op(":=").Op("&").Qual(dynamoPkg, "AttributeValue").Values())
		stmts = append(stmts, jen.Id(vname).Dot("S").Op("=").Qual(awsPkg, "String").Call(jen.Lit(typeName)))
		d[jen.Lit(typeField)] = jen.Id(vname)

		for _, field := range msg.Fields() {
			fext := dynamopb.DynamoFieldOptions{}
			ok, err := field.Extension(dynamopb.E_Field, &fext)
			if err != nil {
				m.Failf("Error: Parsing dynamo.field failed for '%s': %s", field.FullyQualifiedName(), err)
			}

			if !ok {
				m.Debugf("dynamo.field.expose: skipped %s (no extension)", field.FullyQualifiedName())
				continue
			}
			if !fext.Expose {
				m.Debugf("dynamo.field.expose: skipped %s (not exposed)", field.FullyQualifiedName())
				continue
			}

			if fext.Type == nil {
				fext.Type = &dynamopb.Types{}
			}

			pt := field.Type().ProtoType()

			srcName := field.Name().UpperCamelCase().String()
			refId++
			vname := fmt.Sprintf("v%d", refId)
			arrix := fmt.Sprintf("ix%d", refId)
			arrname := fmt.Sprintf("arr%d", refId)

			isArray := field.Type().ProtoLabel() == pgs.Repeated
			if fext.Type.Set && !isArray {
				m.Failf("Error: dynamo.field.set=true, but field is not repeated / array type: '%s'.IsRepeated=%v",
					field.FullyQualifiedName(), field.Type().IsRepeated())
			}

			avt := getAVType(field, &fext)

			switch avt {
			case avt_bytes:
				needNullBoolTrue = true
				stmts = append(stmts, jen.Id(vname).Op(":=").Op("&").Qual(dynamoPkg, "AttributeValue").Values())
				stmts = append(stmts,
					jen.If(jen.Len(jen.Id("p").Dot(field.Name().UpperCamelCase().String())).Op("!=").Lit(0)).Block(
						jen.Id(vname).Dot("B").Op("=").Id("p").Dot(srcName),
					).Else().Block(
						jen.Id(vname).Dot("NULL").Op("=").Op("&").Id("nullBoolTrue"),
					),
				)
				d[jen.Lit(field.Name().LowerSnakeCase().String())] = jen.Id(vname)
			case avt_bool:
				d[jen.Lit(field.Name().LowerSnakeCase().String())] = jen.Op("&").Qual(dynamoPkg, "AttributeValue").Values(jen.Dict{
					jen.Id("BOOL"): jen.Op("&").Id("p").Dot(srcName),
				})
			case avt_list:
				stmts = append(stmts,
					jen.Id(arrname).Op(":=").Make(
						jen.Op("[]*").Qual(dynamoPkg, "AttributeValue"),
						jen.Lit(0),
						jen.Len(jen.Id("p").Dot(srcName)),
					),
				)

				switch {
				case pt.IsInt() || pt == pgs.DoubleT || pt == pgs.FloatT:
					fmtCall := numberFormatStatement(pt, jen.Id(arrix))
					stmts = append(stmts,
						jen.For(jen.List(jen.Id("_"), jen.Id(arrix)).Op(":=").Range().Id("p").Dot(srcName)).Block(
							jen.Id(arrname).Op("=").Append(
								jen.Id(arrname),
								jen.Op("&").Qual(dynamoPkg, "AttributeValue").Values(jen.Dict{
									jen.Id("N"): jen.Qual(awsPkg, "String").Call(
										fmtCall,
									),
								}),
							),
						),
					)
				case pt == pgs.StringT:
					stmts = append(stmts,
						jen.For(jen.List(jen.Id("_"), jen.Id(arrix)).Op(":=").Range().Id("p").Dot(srcName)).Block(
							jen.Id(arrname).Op("=").Append(
								jen.Id(arrname),
								jen.Op("&").Qual(dynamoPkg, "AttributeValue").Values(jen.Dict{
									jen.Id("S"): jen.Qual(awsPkg, "String").Call(
										jen.Id(arrix),
									),
								}),
							),
						),
					)
				default:
					m.Failf("Error: dynamo.field '%s' is repeated, but the '%s' type is not supported", field.FullyQualifiedName(), pt.String())
				}
				d[jen.Lit(field.Name().LowerSnakeCase().String())] = jen.Op("&").Qual(dynamoPkg, "AttributeValue").Values(jen.Dict{
					jen.Id("L"): jen.Id(arrname),
				})
			case avt_map:
				// avt_map: impl
				m.Failf("dynamo.field: not done: avt_map type: %s", field.FullyQualifiedName())
				panic("applyMarshal not done: avt_map")
			case avt_number:
				fmtCall := numberFormatStatement(pt, jen.Id("p").Dot(srcName))
				d[jen.Lit(field.Name().LowerSnakeCase().String())] = jen.Op("&").Qual(dynamoPkg, "AttributeValue").Values(jen.Dict{
					jen.Id("N"): jen.Qual(awsPkg, "String").Call(fmtCall),
				})
			case avt_null:
				// avt_null: unused
			case avt_string:
				needNullBoolTrue = true
				stmts = append(stmts, jen.Id(vname).Op(":=").Op("&").Qual(dynamoPkg, "AttributeValue").Values())
				stmts = append(stmts,
					jen.If(jen.Len(jen.Id("p").Dot(field.Name().UpperCamelCase().String())).Op("!=").Lit(0)).Block(
						jen.Id(vname).Dot("S").Op("=").Qual(awsPkg, "String").Call(jen.Id("p").Dot(srcName)),
					).Else().Block(
						jen.Id(vname).Dot("NULL").Op("=").Op("&").Id("nullBoolTrue"),
					),
				)
				d[jen.Lit(field.Name().LowerSnakeCase().String())] = jen.Id(vname)
			case avt_string_set, avt_number_set, avt_byte_set:
				arrT := jen.Op("[]*").Id("string")
				if avt == avt_byte_set {
					arrT = jen.Op("[][]").Id("byte")
				}
				stmts = append(stmts,
					jen.Id(arrname).Op(":=").Make(
						arrT,
						jen.Lit(0),
						jen.Len(jen.Id("p").Dot(srcName)),
					),
				)
				needNullBoolTrue = true
				setType := ""
				switch avt {
				case avt_number_set:
					setType = "NS"
					fmtCall := numberFormatStatement(pt, jen.Id(arrix))
					stmts = append(stmts,
						jen.For(jen.List(jen.Id("_"), jen.Id(arrix)).Op(":=").Range().Id("p").Dot(srcName)).Block(
							jen.Id(arrname).Op("=").Append(
								jen.Id(arrname),
								jen.Qual(awsPkg, "String").Call(
									fmtCall,
								),
							),
						),
					)
				case avt_string_set:
					setType = "SS"
					stmts = append(stmts,
						jen.For(jen.List(jen.Id("_"), jen.Id(arrix)).Op(":=").Range().Id("p").Dot(srcName)).Block(
							jen.Id(arrname).Op("=").Append(
								jen.Id(arrname),
								jen.Qual(awsPkg, "String").Call(
									jen.Id(arrix),
								),
							),
						),
					)
				case avt_byte_set:
					setType = "BS"
					stmts = append(stmts,
						jen.For(jen.List(jen.Id("_"), jen.Id(arrix)).Op(":=").Range().Id("p").Dot(srcName)).Block(
							jen.Id(arrname).Op("=").Append(
								jen.Id(arrname),
								jen.Id(arrix),
							),
						),
					)
				}

				stmts = append(stmts,
					jen.Var().Id(vname).Op("*").Qual(dynamoPkg, "AttributeValue"),
					jen.If(jen.Len(jen.Id(arrname)).Op("!=").Lit(0)).Block(
						jen.Id(vname).Op("=").Op("&").Qual(dynamoPkg, "AttributeValue").Values(jen.Dict{
							jen.Id(setType): jen.Id(arrname),
						}),
					).Else().Block(
						jen.Id(vname).Dot("NULL").Op("=").Op("&").Id("nullBoolTrue"),
					),
				)
				d[jen.Lit(field.Name().LowerSnakeCase().String())] = jen.Id(vname)
			}
		}

		if needNullBoolTrue {
			stmts = append([]jen.Code{
				jen.Id("nullBoolTrue").Op(":=").True(),
			}, stmts...)
		}

		if needProtoBuffer {
			stmts = append([]jen.Code{
				jen.Id(protoBuffer).Op(":=").Qual(protoPkg, "NewBuffer").Call(jen.Nil()),
			}, stmts...)
		}

		if needErr {
			stmts = append([]jen.Code{
				jen.Op("var").Id("err").Id("error"),
			}, stmts...)
		}

		if needStringBuilder {
			stmts = append([]jen.Code{
				jen.Op("var").Id("sb").Qual(stringsPkg, "Builder"),
			}, stmts...)
		}

		stmts = append(stmts, jen.Id("av").Dot("M").Op("=").Map(jen.String()).Op("*").Qual(dynamoPkg, "AttributeValue").Values(d))

		stmts = append(stmts, jen.Return(jen.Nil()))

		f.Func().Params(
			jen.Id("p").Op("*").Id(structName.String()),
		).Id("MarshalDynamoDBAttributeValue").Params(jen.Id("av").Op("*").Qual(dynamoPkg, "AttributeValue")).Id("error").Block(
			stmts...,
		).Line()
	}
	return nil
}

func (m *Module) applyUnmarshal(f *jen.File, in pgs.File) error {
	for _, msg := range in.AllMessages() {
		structName := m.ctx.Name(msg)
		mext := dynamopb.DynamoMessageOptions{}
		ok, err := msg.Extension(dynamopb.E_Msg, &mext)
		if err != nil {
			m.Logf("Parsing dynamo.msg failed: %s", err)
			m.Fail("code generation failed")
		}
		if ok && mext.Disabled {
			m.Logf("dynamo.msg disabled for %s", structName)
			continue
		}

		stmts := []jen.Code{}

		typeName := fmt.Sprintf("type.googleapis.com/%s.%s", msg.Package().ProtoName().String(), msg.Name())

		stmts = append(stmts,
			jen.List(jen.Id(typeField), jen.Id("ok")).Op(":=").Id("av").Dot("M").Index(jen.Lit(typeField)),
			jen.If(jen.Op("!").Id("ok")).Block(
				jen.Return(jen.Qual(fmtPkg, "Errorf").Call(
					jen.Lit("dyanmo: "+typeField+" missing"),
				),
				)),
			jen.If(jen.Qual(awsPkg, "StringValue").Call(jen.Id(typeField).Dot("S")).Op("!=").Lit(typeName)).Block(
				jen.Return(jen.Qual(fmtPkg, "Errorf").Call(
					jen.Lit(fmt.Sprintf("dyanmo: _type mismatch: %s expected, got: '%s'", typeName, "%s")),
					jen.Id(typeField),
				),
				)),
		)

		stmts = append(stmts,
			jen.List(jen.Id(valueField), jen.Id("ok")).Op(":=").Id("av").Dot("M").Index(jen.Lit(valueField)),
			jen.If(jen.Op("!").Id("ok")).Block(
				jen.Return(jen.Qual(fmtPkg, "Errorf").Call(
					jen.Lit("dyanmo: "+valueField+" missing"),
				),
				)),
			jen.Return(jen.Qual(protoPkg, "Unmarshal").Call(jen.Id(valueField).Dot("B"), jen.Id("p"))),
		)

		f.Func().Params(
			jen.Id("p").Op("*").Id(structName.String()),
		).Id("UnmarshalDynamoDBAttributeValue").Params(jen.Id("av").Op("*").Qual(dynamoPkg, "AttributeValue")).Id("error").Block(
			stmts...,
		).Line()

		f.Func().Params(
			jen.Id("p").Op("*").Id(structName.String()),
		).Id("UnmarshalDynamo").Params(jen.Id("av").Op("*").Qual(dynamoPkg, "AttributeValue")).Id("error").Block(
			jen.Return(jen.Id("p").Dot("UnmarshalDynamoDBAttributeValue").Call(jen.Id("av"))),
		).Line()

	}
	return nil
}

func numberFormatStatement(pt pgs.ProtoType, access *jen.Statement) *jen.Statement {
	var rv *jen.Statement
	switch pt {
	case pgs.DoubleT, pgs.FloatT:
		rv = jen.Qual(strconvPkg, "FormatFloat").Call(
			jen.Id("float64").Call(access),
			jen.LitByte('E'),
			jen.Lit(-1),
			jen.Lit(64),
		)
	case pgs.Int64T, pgs.SFixed64, pgs.SInt64, pgs.Int32T, pgs.SFixed32, pgs.SInt32:
		rv = jen.Qual(strconvPkg, "FormatInt").Call(
			jen.Id("int64").Call(access),
			jen.Lit(10),
		)
	case pgs.UInt64T, pgs.Fixed64T, pgs.UInt32T, pgs.Fixed32T:
		rv = jen.Qual(strconvPkg, "FormatUint").Call(
			jen.Id("uint64").Call(access),
			jen.Lit(10),
		)
	}
	return rv
}

func numberParseStatement(pt pgs.ProtoType, access *jen.Statement) *jen.Statement {
	var rv *jen.Statement
	switch pt {
	case pgs.DoubleT, pgs.FloatT:
		rv = jen.Qual(strconvPkg, "ParseFloat").Call(
			jen.Qual(awsPkg, "StringValue").Call(access),
			jen.Lit(64),
		)
	case pgs.Int64T, pgs.SFixed64, pgs.SInt64, pgs.Int32T, pgs.SFixed32, pgs.SInt32:
		rv = jen.Qual(strconvPkg, "ParseInt").Call(
			jen.Qual(awsPkg, "StringValue").Call(access),
			jen.Lit(10),
			jen.Lit(64),
		)
	case pgs.UInt64T, pgs.Fixed64T, pgs.UInt32T, pgs.Fixed32T:
		rv = jen.Qual(strconvPkg, "ParseUint").Call(
			jen.Qual(awsPkg, "StringValue").Call(access),
			jen.Lit(10),
			jen.Lit(64),
		)
	}
	return rv
}
