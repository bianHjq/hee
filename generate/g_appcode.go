// Copyright 2013 bee authors
//
// Licensed under the Apache License, Version 2.0 (the "License"): you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS, WITHOUT
// WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the
// License for the specific language governing permissions and limitations
// under the License.

package generate

import (
	"database/sql"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"
	beeLogger "github.com/skOak/hee/logger"
	"github.com/skOak/hee/logger/colors"
	"github.com/skOak/hee/utils"
)

const (
	OModel byte = 1 << iota
	OController
	ORouter
)

// DbTransformer has method to reverse engineer a database schema to restful api code
type DbTransformer interface {
	GetTableNames(conn *sql.DB) []string
	GetConstraints(conn *sql.DB, table *Table, blackList map[string]bool)
	GetColumns(conn *sql.DB, table *Table, blackList map[string]bool)
	GetGoDataType(sqlType string) (string, error)
}

// MysqlDB is the MySQL version of DbTransformer
type MysqlDB struct {
}

// PostgresDB is the PostgreSQL version of DbTransformer
type PostgresDB struct {
}

// dbDriver maps a DBMS name to its version of DbTransformer
var dbDriver = map[string]DbTransformer{
	"mysql":    &MysqlDB{},
	"postgres": &PostgresDB{},
}

type MvcPath struct {
	ModelPath      string
	ControllerPath string
	RouterPath     string
}

// typeMapping maps SQL data type to corresponding Go data type
var typeMappingMysql = map[string]string{
	"int":                "int", // int signed
	"integer":            "int",
	"tinyint":            "int8",
	"smallint":           "int16",
	"mediumint":          "int32",
	"bigint":             "int64",
	"int unsigned":       "uint", // int unsigned
	"integer unsigned":   "uint",
	"tinyint unsigned":   "uint8",
	"smallint unsigned":  "uint16",
	"mediumint unsigned": "uint32",
	"bigint unsigned":    "uint64",
	"bit":                "uint64",
	"bool":               "bool",   // boolean
	"enum":               "string", // enum
	"set":                "string", // set
	"varchar":            "string", // string & text
	"char":               "string",
	"tinytext":           "string",
	"mediumtext":         "string",
	"text":               "string",
	"longtext":           "string",
	"blob":               "string", // blob
	"tinyblob":           "string",
	"mediumblob":         "string",
	"longblob":           "string",
	"date":               "time.Time", // time
	"datetime":           "time.Time",
	"timestamp":          "time.Time",
	"time":               "time.Time",
	"float":              "float32", // float & decimal
	"double":             "float64",
	"decimal":            "float64",
	"binary":             "string", // binary
	"varbinary":          "string",
	"year":               "int16",
}

// typeMappingPostgres maps SQL data type to corresponding Go data type
var typeMappingPostgres = map[string]string{
	"serial":                      "int", // serial
	"big serial":                  "int64",
	"smallint":                    "int16", // int
	"integer":                     "int",
	"bigint":                      "int64",
	"boolean":                     "bool",   // bool
	"char":                        "string", // string
	"character":                   "string",
	"character varying":           "string",
	"varchar":                     "string",
	"text":                        "string",
	"date":                        "time.Time", // time
	"time":                        "time.Time",
	"timestamp":                   "time.Time",
	"timestamp without time zone": "time.Time",
	"timestamp with time zone":    "time.Time",
	"interval":                    "string",  // time interval, string for now
	"real":                        "float32", // float & decimal
	"double precision":            "float64",
	"decimal":                     "float64",
	"numeric":                     "float64",
	"money":                       "float64", // money
	"bytea":                       "string",  // binary
	"tsvector":                    "string",  // fulltext
	"ARRAY":                       "string",  // array
	"USER-DEFINED":                "string",  // user defined
	"uuid":                        "string",  // uuid
	"json":                        "string",  // json
	"jsonb":                       "string",  // jsonb
	"inet":                        "string",  // ip address
}

// Table represent a table in a database
type Table struct {
	Name          string
	Pk            string
	PkType        string
	Uk            []string
	Fk            map[string]*ForeignKey
	Columns       []*Column
	ImportTimePkg bool
	IdDelete      bool // 是否存在is_deleleted字段
}

// Column reprsents a column for a table
type Column struct {
	Name string
	Type string
	Tag  *OrmTag
}

// ForeignKey represents a foreign key column for a table
type ForeignKey struct {
	Name      string
	RefSchema string
	RefTable  string
	RefColumn string
}

// OrmTag contains Beego ORM tag information for a column
type OrmTag struct {
	Auto        bool
	Pk          bool
	Null        bool
	Index       bool
	Unique      bool
	Column      string
	Size        string
	Decimals    string
	Digits      string
	AutoNow     bool
	AutoNowAdd  bool
	Type        string
	Default     string
	RelOne      bool
	ReverseOne  bool
	RelFk       bool
	TableFk     string
	ReverseMany bool
	RelM2M      bool
	Comment     string //column comment
}

// String returns the source code string for the Table struct
func (tb *Table) String() string {
	rv := fmt.Sprintf("type %s struct {\n", utils.CamelCase(tb.Name))
	for _, v := range tb.Columns {
		rv += v.String() + "\n"
	}
	rv += "}\n"
	return rv
}

// String returns the source code string of a field in Table struct
// It maps to a column in database table. e.g. Id int `gorm:"column:id;auto"`
func (col *Column) String() string {
	return fmt.Sprintf("%s %s %s", col.Name, col.Type, col.Tag.String())
}

// String returns the ORM tag string for a column
func (tag *OrmTag) String() string {
	var ormOptions []string
	var sqlOptions []string
	if tag.Column != "" {
		ormOptions = append(ormOptions, fmt.Sprintf("column:%s", tag.Column))
	}
	if tag.Auto {
		ormOptions = append(ormOptions, "AUTO_INCREMENT")
	}
	if tag.Size != "" {
		ormOptions = append(ormOptions, fmt.Sprintf("size:%s:", tag.Size))
	}
	if tag.Type != "" {
		ormOptions = append(ormOptions, fmt.Sprintf("type:%s", tag.Type))
	}
	if !tag.Null {
		ormOptions = append(ormOptions, "not null")
	}
	if tag.AutoNow || tag.AutoNowAdd {
		//ormOptions = append(ormOptions, "auto_now")
		sqlOptions = append(sqlOptions, "default:current_timestamp")
	}
	//if tag.AutoNowAdd {
	//	ormOptions = append(ormOptions, "auto_now_add")
	//}
	//if tag.Decimals != "" {
	//	ormOptions = append(ormOptions, fmt.Sprintf("digits(%s);decimals(%s)", tag.Digits, tag.Decimals))
	//}
	if tag.RelFk {
		ormOptions = append(ormOptions, fmt.Sprintf("ForeignKey:%s", tag.TableFk))
	}
	//if tag.RelOne {
	//	ormOptions = append(ormOptions, "rel(one)")
	//}
	//if tag.ReverseOne {
	//	ormOptions = append(ormOptions, "reverse(one)")
	//}
	//if tag.ReverseMany {
	//	ormOptions = append(ormOptions, "reverse(many)")
	//}
	//if tag.RelM2M {
	//	ormOptions = append(ormOptions, "rel(m2m)")
	//}
	if tag.Pk {
		ormOptions = append(ormOptions, "primary_key")
	}
	if tag.Unique {
		ormOptions = append(ormOptions, "unique")
	}
	if tag.Default != "" {
		ormOptions = append(sqlOptions, fmt.Sprintf("default:%s", tag.Default))
	}

	if len(ormOptions) == 0 {
		return ""
	}
	if tag.Comment != "" {
		return fmt.Sprintf("`json:\"%s\" gorm:\"%s\" description:\"%s\"`", tag.Column, strings.Join(ormOptions, ";"), tag.Comment)
	}
	if len(sqlOptions) > 0 {
		return fmt.Sprintf("`json:\"%s\" gorm:\"%s\" sql:\"%s\"`", tag.Column, strings.Join(ormOptions, ";"), strings.Join(sqlOptions, ";"))
	}
	return fmt.Sprintf("`json:\"%s\" gorm:\"%s\"`", tag.Column, strings.Join(ormOptions, ";"))
}

func GenerateAppcode(driver, connStr, level, tables, currpath string) {
	var mode byte
	switch level {
	case "1":
		mode = OModel
	case "2":
		mode = OModel | OController
	case "3":
		mode = OModel | OController | ORouter
	default:
		beeLogger.Log.Fatal("Invalid level value. Must be either \"1\", \"2\", or \"3\"")
	}
	var selectedTables map[string]bool
	if tables != "" {
		selectedTables = make(map[string]bool)
		for _, v := range strings.Split(tables, ",") {
			selectedTables[v] = true
		}
	}
	switch driver {
	case "mysql":
	case "postgres":
	case "sqlite":
		beeLogger.Log.Fatal("Generating app code from SQLite database is not supported yet.")
	default:
		beeLogger.Log.Fatal("Unknown database driver. Must be either \"mysql\", \"postgres\" or \"sqlite\"")
	}
	gen(driver, connStr, mode, selectedTables, currpath)
}

// Generate takes table, column and foreign key information from database connection
// and generate corresponding golang source files
func gen(dbms, connStr string, mode byte, selectedTableNames map[string]bool, apppath string) {
	db, err := sql.Open(dbms, connStr)
	if err != nil {
		beeLogger.Log.Fatalf("Could not connect to '%s' database using '%s': %s", dbms, connStr, err)
	}
	defer db.Close()
	if trans, ok := dbDriver[dbms]; ok {
		beeLogger.Log.Info("Analyzing database tables...")
		var tableNames []string
		if len(selectedTableNames) != 0 {
			for tableName := range selectedTableNames {
				tableNames = append(tableNames, tableName)
			}
		} else {
			tableNames = trans.GetTableNames(db)
		}
		tables := getTableObjects(tableNames, db, trans)
		mvcPath := new(MvcPath)
		mvcPath.ModelPath = path.Join(apppath, "models")
		mvcPath.ControllerPath = path.Join(apppath, "controllers")
		mvcPath.RouterPath = path.Join(apppath, "routers")
		createPaths(mode, mvcPath)
		pkgPath := getPackagePath(apppath)
		writeSourceFiles(dbms, pkgPath, tables, mode, mvcPath, selectedTableNames)
	} else {
		beeLogger.Log.Fatalf("Generating app code from '%s' database is not supported yet.", dbms)
	}
}

// GetTableNames returns a slice of table names in the current database
func (*MysqlDB) GetTableNames(db *sql.DB) (tables []string) {
	rows, err := db.Query("SHOW TABLES")
	if err != nil {
		beeLogger.Log.Fatalf("Could not show tables: %s", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			beeLogger.Log.Fatalf("Could not show tables: %s", err)
		}
		tables = append(tables, name)
	}
	return
}

// getTableObjects process each table name
func getTableObjects(tableNames []string, db *sql.DB, dbTransformer DbTransformer) (tables []*Table) {
	// if a table has a composite pk or doesn't have pk, we can't use it yet
	// these tables will be put into blacklist so that other struct will not
	// reference it.
	blackList := make(map[string]bool)
	// process constraints information for each table, also gather blacklisted table names
	for _, tableName := range tableNames {
		// create a table struct
		tb := new(Table)
		tb.Name = tableName
		tb.Fk = make(map[string]*ForeignKey)
		dbTransformer.GetConstraints(db, tb, blackList)
		tables = append(tables, tb)
	}
	// process columns, ignoring blacklisted tables
	for _, tb := range tables {
		dbTransformer.GetColumns(db, tb, blackList)
	}
	return
}

// GetConstraints gets primary key, unique key and foreign keys of a table from
// information_schema and fill in the Table struct
func (*MysqlDB) GetConstraints(db *sql.DB, table *Table, blackList map[string]bool) {
	rows, err := db.Query(
		`SELECT
			c.constraint_type, u.column_name, u.referenced_table_schema, u.referenced_table_name, referenced_column_name, u.ordinal_position
		FROM
			information_schema.table_constraints c
		INNER JOIN
			information_schema.key_column_usage u ON c.constraint_name = u.constraint_name
		WHERE
			c.table_schema = database() AND c.table_name = ? AND u.table_schema = database() AND u.table_name = ?`,
		table.Name, table.Name) //  u.position_in_unique_constraint,
	if err != nil {
		beeLogger.Log.Fatal("Could not query INFORMATION_SCHEMA for PK/UK/FK information")
	}
	for rows.Next() {
		var constraintTypeBytes, columnNameBytes, refTableSchemaBytes, refTableNameBytes, refColumnNameBytes, refOrdinalPosBytes []byte
		if err := rows.Scan(&constraintTypeBytes, &columnNameBytes, &refTableSchemaBytes, &refTableNameBytes, &refColumnNameBytes, &refOrdinalPosBytes); err != nil {
			beeLogger.Log.Fatal("Could not read INFORMATION_SCHEMA for PK/UK/FK information")
		}
		constraintType, columnName, refTableSchema, refTableName, refColumnName, refOrdinalPos :=
			string(constraintTypeBytes), string(columnNameBytes), string(refTableSchemaBytes),
			string(refTableNameBytes), string(refColumnNameBytes), string(refOrdinalPosBytes)
		if constraintType == "PRIMARY KEY" {
			if refOrdinalPos == "1" {
				table.Pk = columnName
			} else {
				table.Pk = ""
				// Add table to blacklist so that other struct will not reference it, because we are not
				// registering blacklisted tables
				blackList[table.Name] = true
			}
		} else if constraintType == "UNIQUE" {
			table.Uk = append(table.Uk, columnName)
		} else if constraintType == "FOREIGN KEY" {
			fk := new(ForeignKey)
			fk.Name = columnName
			fk.RefSchema = refTableSchema
			fk.RefTable = refTableName
			fk.RefColumn = refColumnName
			table.Fk[columnName] = fk
		}
	}
}

// GetColumns retrieves columns details from
// information_schema and fill in the Column struct
func (mysqlDB *MysqlDB) GetColumns(db *sql.DB, table *Table, blackList map[string]bool) {
	// retrieve columns
	colDefRows, err := db.Query(
		`SELECT
			column_name, data_type, column_type, is_nullable, column_default, extra, column_comment 
		FROM
			information_schema.columns
		WHERE
			table_schema = database() AND table_name = ?`,
		table.Name)
	if err != nil {
		beeLogger.Log.Fatalf("Could not query the database: %s", err)
	}
	defer colDefRows.Close()

	for colDefRows.Next() {
		// datatype as bytes so that SQL <null> values can be retrieved
		var colNameBytes, dataTypeBytes, columnTypeBytes, isNullableBytes, columnDefaultBytes, extraBytes, columnCommentBytes []byte
		if err := colDefRows.Scan(&colNameBytes, &dataTypeBytes, &columnTypeBytes, &isNullableBytes, &columnDefaultBytes, &extraBytes, &columnCommentBytes); err != nil {
			beeLogger.Log.Fatal("Could not query INFORMATION_SCHEMA for column information")
		}
		colName, dataType, columnType, isNullable, columnDefault, extra, columnComment :=
			string(colNameBytes), string(dataTypeBytes), string(columnTypeBytes), string(isNullableBytes), string(columnDefaultBytes), string(extraBytes), string(columnCommentBytes)

		// create a column
		col := new(Column)
		col.Name = utils.CamelCase(colName)
		col.Type, err = mysqlDB.GetGoDataType(dataType)
		if err != nil {
			beeLogger.Log.Fatalf("%s", err)
		}
		if colName == "is_deleted" {
			// 如果存在该列，则会记录需要用这个字段来代表删除动作
			table.IdDelete = true
		}
		if isSQLSignedIntType(dataType) {
			sign := extractIntSignness(columnType)
			if sign == "unsigned" {
				col.Type, err = mysqlDB.GetGoDataType(dataType + " " + sign)
				if err != nil {
					beeLogger.Log.Fatalf("%s", err)
				}
			}
		}

		// Tag info
		tag := new(OrmTag)
		tag.Column = colName
		tag.Comment = columnComment
		if table.Pk == colName {
			col.Name = "Id"
			//col.Type = "int"
			table.PkType = col.Type
			if extra == "auto_increment" {
				tag.Auto = true
			} else {
				tag.Pk = true
			}
		} else {
			fkCol, isFk := table.Fk[colName]
			isBl := false
			if isFk {
				_, isBl = blackList[fkCol.RefTable]
			}
			// check if the current column is a foreign key
			if isFk && !isBl {
				tag.RelFk = true
				refStructName := fkCol.RefTable
				tag.TableFk = refStructName
				col.Name = utils.CamelCase(colName)
				col.Type = "*" + utils.CamelCase(refStructName)
			} else {
				// if the name of column is Id, and it's not primary key
				if colName == "id" {
					col.Name = "Id_RENAME"
				}
				if isNullable == "YES" {
					tag.Null = true
				}
				if isSQLStringType(dataType) {
					tag.Size = extractColSize(columnType)
				}
				if isSQLTemporalType(dataType) {
					tag.Type = dataType
					//check auto_now, auto_now_add
					if columnDefault == "CURRENT_TIMESTAMP" && extra == "on update CURRENT_TIMESTAMP" {
						tag.AutoNow = true
					} else if columnDefault == "CURRENT_TIMESTAMP" {
						tag.AutoNowAdd = true
					}
					// need to import time package
					table.ImportTimePkg = true
				}
				if isSQLDecimal(dataType) {
					tag.Digits, tag.Decimals = extractDecimal(columnType)
				}
				if isSQLBinaryType(dataType) {
					tag.Size = extractColSize(columnType)
				}
				if isSQLBitType(dataType) {
					tag.Size = extractColSize(columnType)
				}
			}
		}
		col.Tag = tag
		table.Columns = append(table.Columns, col)
	}
}

// GetGoDataType maps an SQL data type to Golang data type
func (*MysqlDB) GetGoDataType(sqlType string) (string, error) {
	if v, ok := typeMappingMysql[sqlType]; ok {
		return v, nil
	}
	return "", fmt.Errorf("data type '%s' not found", sqlType)
}

// GetTableNames for PostgreSQL
func (*PostgresDB) GetTableNames(db *sql.DB) (tables []string) {
	rows, err := db.Query(`
		SELECT table_name FROM information_schema.tables
		WHERE table_catalog = current_database() AND
		table_type = 'BASE TABLE' AND
		table_schema NOT IN ('pg_catalog', 'information_schema')`)
	if err != nil {
		beeLogger.Log.Fatalf("Could not show tables: %s", err)
	}
	defer rows.Close()

	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			beeLogger.Log.Fatalf("Could not show tables: %s", err)
		}
		tables = append(tables, name)
	}
	return
}

// GetConstraints for PostgreSQL
func (*PostgresDB) GetConstraints(db *sql.DB, table *Table, blackList map[string]bool) {
	rows, err := db.Query(
		`SELECT
			c.constraint_type,
			u.column_name,
			cu.table_catalog AS referenced_table_catalog,
			cu.table_name AS referenced_table_name,
			cu.column_name AS referenced_column_name,
			u.ordinal_position
		FROM
			information_schema.table_constraints c
		INNER JOIN
			information_schema.key_column_usage u ON c.constraint_name = u.constraint_name
		INNER JOIN
			information_schema.constraint_column_usage cu ON cu.constraint_name =  c.constraint_name
		WHERE
			c.table_catalog = current_database() AND c.table_schema NOT IN ('pg_catalog', 'information_schema')
			 AND c.table_name = $1
			AND u.table_catalog = current_database() AND u.table_schema NOT IN ('pg_catalog', 'information_schema')
			 AND u.table_name = $2`,
		table.Name, table.Name) //  u.position_in_unique_constraint,
	if err != nil {
		beeLogger.Log.Fatalf("Could not query INFORMATION_SCHEMA for PK/UK/FK information: %s", err)
	}

	for rows.Next() {
		var constraintTypeBytes, columnNameBytes, refTableSchemaBytes, refTableNameBytes, refColumnNameBytes, refOrdinalPosBytes []byte
		if err := rows.Scan(&constraintTypeBytes, &columnNameBytes, &refTableSchemaBytes, &refTableNameBytes, &refColumnNameBytes, &refOrdinalPosBytes); err != nil {
			beeLogger.Log.Fatalf("Could not read INFORMATION_SCHEMA for PK/UK/FK information: %s", err)
		}
		constraintType, columnName, refTableSchema, refTableName, refColumnName, refOrdinalPos :=
			string(constraintTypeBytes), string(columnNameBytes), string(refTableSchemaBytes),
			string(refTableNameBytes), string(refColumnNameBytes), string(refOrdinalPosBytes)
		if constraintType == "PRIMARY KEY" {
			if refOrdinalPos == "1" {
				table.Pk = columnName
			} else {
				table.Pk = ""
				// add table to blacklist so that other struct will not reference it, because we are not
				// registering blacklisted tables
				blackList[table.Name] = true
			}
		} else if constraintType == "UNIQUE" {
			table.Uk = append(table.Uk, columnName)
		} else if constraintType == "FOREIGN KEY" {
			fk := new(ForeignKey)
			fk.Name = columnName
			fk.RefSchema = refTableSchema
			fk.RefTable = refTableName
			fk.RefColumn = refColumnName
			table.Fk[columnName] = fk
		}
	}
}

// GetColumns for PostgreSQL
func (postgresDB *PostgresDB) GetColumns(db *sql.DB, table *Table, blackList map[string]bool) {
	// retrieve columns
	colDefRows, err := db.Query(
		`SELECT
			column_name,
			data_type,
			data_type ||
			CASE
				WHEN data_type = 'character' THEN '('||character_maximum_length||')'
				WHEN data_type = 'numeric' THEN '(' || numeric_precision || ',' || numeric_scale ||')'
				ELSE ''
			END AS column_type,
			is_nullable,
			column_default,
			'' AS extra
		FROM
			information_schema.columns
		WHERE
			table_catalog = current_database() AND table_schema NOT IN ('pg_catalog', 'information_schema')
			 AND table_name = $1`,
		table.Name)
	if err != nil {
		beeLogger.Log.Fatalf("Could not query INFORMATION_SCHEMA for column information: %s", err)
	}
	defer colDefRows.Close()

	for colDefRows.Next() {
		// datatype as bytes so that SQL <null> values can be retrieved
		var colNameBytes, dataTypeBytes, columnTypeBytes, isNullableBytes, columnDefaultBytes, extraBytes []byte
		if err := colDefRows.Scan(&colNameBytes, &dataTypeBytes, &columnTypeBytes, &isNullableBytes, &columnDefaultBytes, &extraBytes); err != nil {
			beeLogger.Log.Fatalf("Could not query INFORMATION_SCHEMA for column information: %s", err)
		}
		colName, dataType, columnType, isNullable, columnDefault, extra :=
			string(colNameBytes), string(dataTypeBytes), string(columnTypeBytes), string(isNullableBytes), string(columnDefaultBytes), string(extraBytes)
		// Create a column
		col := new(Column)
		col.Name = utils.CamelCase(colName)
		col.Type, err = postgresDB.GetGoDataType(dataType)
		if err != nil {
			beeLogger.Log.Fatalf("%s", err)
		}
		if colName == "is_deleted" {
			// 如果存在该列，则会记录需要用这个字段来代表删除动作
			table.IdDelete = true
		}

		// Tag info
		tag := new(OrmTag)
		tag.Column = colName
		if table.Pk == colName {
			col.Name = "Id"
			col.Type = "int"
			if extra == "auto_increment" {
				tag.Auto = true
			} else {
				tag.Pk = true
			}
		} else {
			fkCol, isFk := table.Fk[colName]
			isBl := false
			if isFk {
				_, isBl = blackList[fkCol.RefTable]
			}
			// check if the current column is a foreign key
			if isFk && !isBl {
				tag.RelFk = true
				refStructName := fkCol.RefTable
				tag.TableFk = refStructName
				col.Name = utils.CamelCase(colName)
				col.Type = "*" + utils.CamelCase(refStructName)
			} else {
				// if the name of column is Id, and it's not primary key
				if colName == "id" {
					col.Name = "Id_RENAME"
				}
				if isNullable == "YES" {
					tag.Null = true
				}
				if isSQLStringType(dataType) {
					tag.Size = extractColSize(columnType)
				}
				if isSQLTemporalType(dataType) || strings.HasPrefix(dataType, "timestamp") {
					tag.Type = dataType
					//check auto_now, auto_now_add
					if columnDefault == "CURRENT_TIMESTAMP" && extra == "on update CURRENT_TIMESTAMP" {
						tag.AutoNow = true
					} else if columnDefault == "CURRENT_TIMESTAMP" {
						tag.AutoNowAdd = true
					}
					// need to import time package
					table.ImportTimePkg = true
				}
				if isSQLDecimal(dataType) {
					tag.Digits, tag.Decimals = extractDecimal(columnType)
				}
				if isSQLBinaryType(dataType) {
					tag.Size = extractColSize(columnType)
				}
				if isSQLStrangeType(dataType) {
					tag.Type = dataType
				}
			}
		}
		col.Tag = tag
		table.Columns = append(table.Columns, col)
	}
}

// GetGoDataType returns the Go type from the mapped Postgres type
func (*PostgresDB) GetGoDataType(sqlType string) (string, error) {
	if v, ok := typeMappingPostgres[sqlType]; ok {
		return v, nil
	}
	return "", fmt.Errorf("data type '%s' not found", sqlType)
}

// deleteAndRecreatePaths removes several directories completely
func createPaths(mode byte, paths *MvcPath) {
	if (mode & OModel) == OModel {
		os.Mkdir(paths.ModelPath, 0777)
	}
	if (mode & OController) == OController {
		os.Mkdir(paths.ControllerPath, 0777)
	}
	if (mode & ORouter) == ORouter {
		os.Mkdir(paths.RouterPath, 0777)
	}
}

// writeSourceFiles generates source files for model/controller/router
// It will wipe the following directories and recreate them:./models, ./controllers, ./routers
// Newly geneated files will be inside these folders.
func writeSourceFiles(dbms, pkgPath string, tables []*Table, mode byte, paths *MvcPath, selectedTables map[string]bool) {
	if (OModel & mode) == OModel {
		beeLogger.Log.Info("Creating model files...")
		writeModelFiles(dbms, tables, paths.ModelPath, selectedTables)
	}
	if (OController & mode) == OController {
		beeLogger.Log.Info("Creating controller files...")
		writeControllerFiles(tables, paths.ControllerPath, selectedTables, pkgPath)
	}
	if (ORouter & mode) == ORouter {
		beeLogger.Log.Info("Creating router files...")
		writeRouterFile(tables, paths.RouterPath, selectedTables, pkgPath)
	}
}

// writeModelFiles generates model files
func writeModelFiles(dbms string, tables []*Table, mPath string, selectedTables map[string]bool) {
	w := colors.NewColorWriter(os.Stdout)

	for _, tb := range tables {
		// if selectedTables map is not nil and this table is not selected, ignore it
		if selectedTables != nil {
			if _, selected := selectedTables[tb.Name]; !selected {
				continue
			}
		}
		filename := getFileName(tb.Name)
		fpath := path.Join(mPath, filename+".go")
		var f *os.File
		var err error
		if utils.IsExist(fpath) {
			beeLogger.Log.Warnf("'%s' already exists. Do you want to overwrite it? [Yes|No] ", fpath)
			if utils.AskForConfirmation() {
				f, err = os.OpenFile(fpath, os.O_RDWR|os.O_TRUNC, 0666)
				if err != nil {
					beeLogger.Log.Warnf("%s", err)
					continue
				}
			} else {
				beeLogger.Log.Warnf("Skipped create file '%s'", fpath)
				continue
			}
		} else {
			f, err = os.OpenFile(fpath, os.O_CREATE|os.O_RDWR, 0666)
			if err != nil {
				beeLogger.Log.Warnf("%s", err)
				continue
			}
		}
		var tmpl string
		if tb.Pk == "" {
			tmpl = StructModelTPL
		} else {
			tmpl = ModelTPL
		}
		fileStr := strings.Replace(tmpl, "{{modelStruct}}", tb.String(), 1)
		fileStr = strings.Replace(fileStr, "{{modelName}}", utils.CamelCase(tb.Name), -1)
		fileStr = strings.Replace(fileStr, "{{tableName}}", tb.Name, -1)
		fileStr = strings.Replace(fileStr, "{{pkType}}", tb.PkType, -1)

		// If table contains time field, import time.Time package
		//timePkg := ""
		//importTimePkg := ""
		//if tb.ImportTimePkg {
		//	timePkg = "\"time\"\n"
		//	importTimePkg = "import \"time\"\n"
		//}
		//fileStr = strings.Replace(fileStr, "{{timePkg}}", timePkg, -1)
		//fileStr = strings.Replace(fileStr, "{{importTimePkg}}", importTimePkg, -1)
		//if _, err := f.WriteString(fileStr); err != nil {
		//	beeLogger.Log.Fatalf("Could not write model file to '%s': %s", fpath, err)
		//}
		t, err := template.New("").Parse(fileStr)
		if err != nil {
			beeLogger.Log.Fatalf("new template fileStr failed <%s>", err)
		}
		err = t.Execute(f, tb)
		if err != nil {
			beeLogger.Log.Fatalf("execute template fileStr failed <%s>", err)
			f.Truncate(0)
		}
		utils.CloseFile(f)
		fmt.Fprintf(w, "\t%s%screate%s\t %s%s\n", "\x1b[32m", "\x1b[1m", "\x1b[21m", fpath, "\x1b[0m")
		utils.FormatSourceCode(fpath)
	}

	//generate models.go
	fpath := path.Join(mPath, "models.go")
	var f *os.File
	var err error
	if utils.IsExist(fpath) {
		beeLogger.Log.Warnf("'%s' already exists. Do you want to overwrite it? [Yes|No] ", fpath)
		if utils.AskForConfirmation() {
			f, err = os.OpenFile(fpath, os.O_RDWR|os.O_TRUNC, 0666)
			if err != nil {
				beeLogger.Log.Warnf("%s", err)
				return
			}
		} else {
			beeLogger.Log.Warnf("Skipped create file '%s'", fpath)
			return
		}
	} else {
		f, err = os.OpenFile(fpath, os.O_CREATE|os.O_RDWR, 0666)
		if err != nil {
			beeLogger.Log.Warnf("%s", err)
			return
		}
	}

	t, err := template.New("").Parse(ModelsTPL)
	if err != nil {
		beeLogger.Log.Fatalf("template ModelsTPL faield <%s>", err)
		utils.CloseFile(f)
		return
	}
	err = t.Execute(f, &struct{ Dialect string }{dbms})
	if err != nil {
		beeLogger.Log.Fatalf("template ModelsTPL faield <%s>", err)
		f.Truncate(0)
		utils.CloseFile(f)
		return
	}
	utils.CloseFile(f)
	fmt.Fprintf(w, "\t%s%screate%s\t %s%s\n", "\x1b[32m", "\x1b[1m", "\x1b[21m", fpath, "\x1b[0m")
	utils.FormatSourceCode(fpath)
}

// writeControllerFiles generates controller files
func writeControllerFiles(tables []*Table, cPath string, selectedTables map[string]bool, pkgPath string) {
	w := colors.NewColorWriter(os.Stdout)

	for _, tb := range tables {
		// If selectedTables map is not nil and this table is not selected, ignore it
		if selectedTables != nil {
			if _, selected := selectedTables[tb.Name]; !selected {
				continue
			}
		}
		if tb.Pk == "" {
			continue
		}
		filename := getFileName(tb.Name)
		fpath := path.Join(cPath, filename+".go")
		var f *os.File
		var err error
		if utils.IsExist(fpath) {
			beeLogger.Log.Warnf("'%s' already exists. Do you want to overwrite it? [Yes|No] ", fpath)
			if utils.AskForConfirmation() {
				f, err = os.OpenFile(fpath, os.O_RDWR|os.O_TRUNC, 0666)
				if err != nil {
					beeLogger.Log.Warnf("%s", err)
					continue
				}
			} else {
				beeLogger.Log.Warnf("Skipped create file '%s'", fpath)
				continue
			}
		} else {
			f, err = os.OpenFile(fpath, os.O_CREATE|os.O_RDWR, 0666)
			if err != nil {
				beeLogger.Log.Warnf("%s", err)
				continue
			}
		}
		fileStr := strings.Replace(CtrlTPL, "{{ctrlName}}", utils.CamelCase(tb.Name), -1)
		fileStr = strings.Replace(fileStr, "{{pkgPath}}", pkgPath, -1)
		if _, err := f.WriteString(fileStr); err != nil {
			beeLogger.Log.Fatalf("Could not write controller file to '%s': %s", fpath, err)
		}
		utils.CloseFile(f)
		fmt.Fprintf(w, "\t%s%screate%s\t %s%s\n", "\x1b[32m", "\x1b[1m", "\x1b[21m", fpath, "\x1b[0m")
		utils.FormatSourceCode(fpath)
	}
}

// writeRouterFile generates router file
func writeRouterFile(tables []*Table, rPath string, selectedTables map[string]bool, pkgPath string) {
	w := colors.NewColorWriter(os.Stdout)

	var nameSpaces []string
	for _, tb := range tables {
		// If selectedTables map is not nil and this table is not selected, ignore it
		if selectedTables != nil {
			if _, selected := selectedTables[tb.Name]; !selected {
				continue
			}
		}
		if tb.Pk == "" {
			continue
		}
		// Add namespaces
		nameSpace := strings.Replace(NamespaceTPL, "{{nameSpace}}", tb.Name, -1)
		nameSpace = strings.Replace(nameSpace, "{{ctrlName}}", utils.CamelCase(tb.Name), -1)
		nameSpaces = append(nameSpaces, nameSpace)
	}
	// Add export controller
	fpath := filepath.Join(rPath, "router.go")
	routerStr := strings.Replace(RouterTPL, "{{nameSpaces}}", strings.Join(nameSpaces, ""), 1)
	routerStr = strings.Replace(routerStr, "{{pkgPath}}", pkgPath, 1)
	var f *os.File
	var err error
	if utils.IsExist(fpath) {
		beeLogger.Log.Warnf("'%s' already exists. Do you want to overwrite it? [Yes|No] ", fpath)
		if utils.AskForConfirmation() {
			f, err = os.OpenFile(fpath, os.O_RDWR|os.O_TRUNC, 0666)
			if err != nil {
				beeLogger.Log.Warnf("%s", err)
				return
			}
		} else {
			beeLogger.Log.Warnf("Skipped create file '%s'", fpath)
			return
		}
	} else {
		f, err = os.OpenFile(fpath, os.O_CREATE|os.O_RDWR, 0666)
		if err != nil {
			beeLogger.Log.Warnf("%s", err)
			return
		}
	}
	if _, err := f.WriteString(routerStr); err != nil {
		beeLogger.Log.Fatalf("Could not write router file to '%s': %s", fpath, err)
	}
	utils.CloseFile(f)
	fmt.Fprintf(w, "\t%s%screate%s\t %s%s\n", "\x1b[32m", "\x1b[1m", "\x1b[21m", fpath, "\x1b[0m")
	utils.FormatSourceCode(fpath)
}

func isSQLTemporalType(t string) bool {
	return t == "date" || t == "datetime" || t == "timestamp" || t == "time"
}

func isSQLStringType(t string) bool {
	return t == "char" || t == "varchar"
}

func isSQLSignedIntType(t string) bool {
	return t == "int" || t == "tinyint" || t == "smallint" || t == "mediumint" || t == "bigint"
}

func isSQLDecimal(t string) bool {
	return t == "decimal"
}

func isSQLBinaryType(t string) bool {
	return t == "binary" || t == "varbinary"
}

func isSQLBitType(t string) bool {
	return t == "bit"
}
func isSQLStrangeType(t string) bool {
	return t == "interval" || t == "uuid" || t == "json"
}

// extractColSize extracts field size: e.g. varchar(255) => 255
func extractColSize(colType string) string {
	regex := regexp.MustCompile(`^[a-z]+\(([0-9]+)\)$`)
	size := regex.FindStringSubmatch(colType)
	return size[1]
}

func extractIntSignness(colType string) string {
	regex := regexp.MustCompile(`(int|smallint|mediumint|bigint)\([0-9]+\)(.*)`)
	signRegex := regex.FindStringSubmatch(colType)
	return strings.Trim(signRegex[2], " ")
}

func extractDecimal(colType string) (digits string, decimals string) {
	decimalRegex := regexp.MustCompile(`decimal\(([0-9]+),([0-9]+)\)`)
	decimal := decimalRegex.FindStringSubmatch(colType)
	digits, decimals = decimal[1], decimal[2]
	return
}

func getFileName(tbName string) (filename string) {
	// avoid test file
	filename = tbName
	for strings.HasSuffix(filename, "_test") {
		pos := strings.LastIndex(filename, "_")
		filename = filename[:pos] + filename[pos+1:]
	}
	return
}

func getPackagePath(curpath string) (packpath string) {
	gopath := os.Getenv("GOPATH")
	if gopath == "" {
		beeLogger.Log.Fatal("GOPATH environment variable is not set or empty")
	}

	beeLogger.Log.Debugf("GOPATH: %s", utils.FILE(), utils.LINE(), gopath)

	appsrcpath := ""
	haspath := false
	wgopath := filepath.SplitList(gopath)

	for _, wg := range wgopath {
		wg, _ = filepath.EvalSymlinks(filepath.Join(wg, "src"))
		if strings.HasPrefix(strings.ToLower(curpath), strings.ToLower(wg)) {
			haspath = true
			appsrcpath = wg
			break
		}
	}

	if !haspath {
		beeLogger.Log.Fatalf("Cannot generate application code outside of GOPATH '%s' compare with CWD '%s'", gopath, curpath)
	}

	if curpath == appsrcpath {
		beeLogger.Log.Fatal("Cannot generate application code outside of application path")
	}

	packpath = strings.Join(strings.Split(curpath[len(appsrcpath)+1:], string(filepath.Separator)), "/")
	return
}

const (
	StructModelTPL = `package models
{{if .ImportTimePkg}}
import (
	"time"
)
{{end}}
{{modelStruct}}
`

	ModelTPL = `package models
import (
{{if .ImportTimePkg}}
	"time"

{{end}}
	"github.com/jinzhu/gorm"
)

{{modelStruct}}

func ({{modelName}}) TableName() string {
	return "{{tableName}}"
}

// Add{{modelName}} insert a new {{modelName}} into database and returns
// last inserted Id on success.
func Add{{modelName}}(tx *gorm.DB, m *{{modelName}}) (id {{pkType}}, err error) {
    db := tx
    if db == nil {
        db = DB()
    }
	err = db.Create(m).Error
	if err != nil {
		return 0, err
	}
	return m.Id, nil
}

{{if .IdDelete}}
// Get{{modelName}}ById retrieves {{modelName}} by Id(not deleted). Returns error if
// Id doesn't exist
func Get{{modelName}}ById(tx *gorm.DB, id {{pkType}}) (v *{{modelName}}, err error) {
	db := tx
	if db == nil {
		db = DB()
	}
	v = &{{modelName}}{Id: id}
	err = db.Where("is_deleted=?", 0).First(v).Error
	return
}

// Get{{modelName}}ById retrieves {{modelName}} by Id(including deleted). Returns error if
// Id doesn't exist
func Get{{modelName}}ByIdIncludingDeleted(tx *gorm.DB, id {{pkType}}) (v *{{modelName}}, err error) {
	db := tx
	if db == nil {
		db = DB()
	}
	v = &{{modelName}}{Id: id}
	err = db.First(v).Error
	return
}
{{else}}
// Get{{modelName}}ById retrieves {{modelName}} by Id. Returns error if
// Id doesn't exist
func Get{{modelName}}ById(tx *gorm.DB, id {{pkType}}) (v *{{modelName}}, err error) {
    db := tx
    if db == nil {
        db = DB() }
	v = &{{modelName}}{Id: id}
	err = db.First(v).Error
	return
}
{{end}}

// Search{{modelName}}s retrieves all {{modelName}}(not deleted recoreds) matches certain condition. Returns empty list if
// no records exist
func Search{{modelName}}s(tx *gorm.DB, order string, offset, limit uint64, query string, queryArgs ...interface{}) (ml []*{{modelName}}, err error) {
	{{if .IdDelete}}if query != "" {
		query += " and is_deleted = 0"
	} else {
		query = "is_deleted = 0"
	}
	{{end}}db := tx
    if db == nil {
        db = DB()
    }
	qs := db.Where(query, queryArgs...)
	if order != "" {
		qs = qs.Order(order)
	}
	if offset > 0 {
		qs = qs.Offset(offset)
	}
	if limit > 0 {
		qs = qs.Limit(limit)
	}
	ml = make([]*{{modelName}}, 0)
	err = qs.Find(&ml).Error
	return
}
// Count{{modelName}}s retrieves count of all {{modelName}}(not deleted recoreds) matches certain condition. Returns 0 if
// no records exist
func Count{{modelName}}s(tx *gorm.DB, query string, queryArgs ...interface{}) (count int64, err error) {
	{{if .IdDelete}}if query != "" {
		query += " and is_deleted = 0"
	} else {
		query = "is_deleted = 0"
	}
	{{end}}db := tx
    if db == nil {
        db = DB()
    }
	err = db.Model(&{{modelName}}{}).Where(query, queryArgs...).Count(&count).Error
	return
}

// Update{{modelName}} updates {{modelName}}(all changed fields) by Id and returns error if
// the record to be updated doesn't exist
func Update{{modelName}}ById(tx *gorm.DB, m *{{modelName}}) (err error) {
    db := tx
    if db == nil {
        db = DB()
    }
	return db.Save(m).Error
}

// BatchUpdate{{modelName}}s updates all qualified {{modelName}}s
// return the record number affected and error
func BatchUpdate{{modelName}}s(tx *gorm.DB, kvs map[string]interface{}, query string, queryArgs ...interface{}) (affected int64, err error) {
	if len(kvs) == 0 || query == "" {
		// nothing to update, omit
		return
	}
    db := tx
    if db == nil {
        db = DB()
    }
	ret := db.Table("{{.Name}}").Where(query, queryArgs...).Updates(kvs)
	return ret.RowsAffected, ret.Error
}

// Delete{{modelName}} deletes {{modelName}}(set IsDeleted to 1) by Id and returns error if
// the record to be deleted doesn't exist
func Delete{{modelName}}(tx *gorm.DB, id {{pkType}}) (err error) {
	// ascertain id exists in the database
    db := tx
    if db == nil {
        db = DB()
    }
	v := {{modelName}}{Id: id}
    if err = db.First(&v).Error; err == nil {
        {{if .IdDelete}}v.IsDeleted = 1
        return db.Save(&v).Error
        {{else}}return db.Delete(&v).Error{{end}}
    }
	return
}
`
	CtrlTPL = `package controllers

import (
	"{{pkgPath}}/models"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/astaxie/beego"
)

// {{ctrlName}}Controller operations for {{ctrlName}}
type {{ctrlName}}Controller struct {
	beego.Controller
}

// URLMapping ...
func (c *{{ctrlName}}Controller) URLMapping() {
	c.Mapping("Post", c.Post)
	c.Mapping("GetOne", c.GetOne)
	c.Mapping("GetAll", c.GetAll)
	c.Mapping("Put", c.Put)
	c.Mapping("Delete", c.Delete)
}

// Post ...
// @Title Post
// @Description create {{ctrlName}}
// @Param	body		body 	models.{{ctrlName}}	true		"body for {{ctrlName}} content"
// @Success 201 {int} models.{{ctrlName}}
// @Failure 403 body is empty
// @router / [post]
func (c *{{ctrlName}}Controller) Post() {
	var v models.{{ctrlName}}
	if err := json.Unmarshal(c.Ctx.Input.RequestBody, &v); err == nil {
		if _, err := models.Add{{ctrlName}}(&v); err == nil {
			c.Ctx.Output.SetStatus(201)
			c.Data["json"] = v
		} else {
			c.Data["json"] = err.Error()
		}
	} else {
		c.Data["json"] = err.Error()
	}
	c.ServeJSON()
}

// GetOne ...
// @Title Get One
// @Description get {{ctrlName}} by id
// @Param	id		path 	string	true		"The key for staticblock"
// @Success 200 {object} models.{{ctrlName}}
// @Failure 403 :id is empty
// @router /:id [get]
func (c *{{ctrlName}}Controller) GetOne() {
	idStr := c.Ctx.Input.Param(":id")
	id, _ := strconv.Atoi(idStr)
	v, err := models.Get{{ctrlName}}ById(id)
	if err != nil {
		c.Data["json"] = err.Error()
	} else {
		c.Data["json"] = v
	}
	c.ServeJSON()
}

// GetAll ...
// @Title Get All
// @Description get {{ctrlName}}
// @Param	query	query	string	false	"Filter. e.g. col1:v1,col2:v2 ..."
// @Param	fields	query	string	false	"Fields returned. e.g. col1,col2 ..."
// @Param	sortby	query	string	false	"Sorted-by fields. e.g. col1,col2 ..."
// @Param	order	query	string	false	"Order corresponding to each sortby field, if single value, apply to all sortby fields. e.g. desc,asc ..."
// @Param	limit	query	string	false	"Limit the size of result set. Must be an integer"
// @Param	offset	query	string	false	"Start position of result set. Must be an integer"
// @Success 200 {object} models.{{ctrlName}}
// @Failure 403
// @router / [get]
func (c *{{ctrlName}}Controller) GetAll() {
	var fields []string
	var sortby []string
	var order []string
	var query = make(map[string]string)
	var limit int64 = 10
	var offset int64

	// fields: col1,col2,entity.col3
	if v := c.GetString("fields"); v != "" {
		fields = strings.Split(v, ",")
	}
	// limit: 10 (default is 10)
	if v, err := c.GetInt64("limit"); err == nil {
		limit = v
	}
	// offset: 0 (default is 0)
	if v, err := c.GetInt64("offset"); err == nil {
		offset = v
	}
	// sortby: col1,col2
	if v := c.GetString("sortby"); v != "" {
		sortby = strings.Split(v, ",")
	}
	// order: desc,asc
	if v := c.GetString("order"); v != "" {
		order = strings.Split(v, ",")
	}
	// query: k:v,k:v
	if v := c.GetString("query"); v != "" {
		for _, cond := range strings.Split(v, ",") {
			kv := strings.SplitN(cond, ":", 2)
			if len(kv) != 2 {
				c.Data["json"] = errors.New("Error: invalid query key/value pair")
				c.ServeJSON()
				return
			}
			k, v := kv[0], kv[1]
			query[k] = v
		}
	}

	l, err := models.GetAll{{ctrlName}}(query, fields, sortby, order, offset, limit)
	if err != nil {
		c.Data["json"] = err.Error()
	} else {
		c.Data["json"] = l
	}
	c.ServeJSON()
}

// Put ...
// @Title Put
// @Description update the {{ctrlName}}
// @Param	id		path 	string	true		"The id you want to update"
// @Param	body		body 	models.{{ctrlName}}	true		"body for {{ctrlName}} content"
// @Success 200 {object} models.{{ctrlName}}
// @Failure 403 :id is not int
// @router /:id [put]
func (c *{{ctrlName}}Controller) Put() {
	idStr := c.Ctx.Input.Param(":id")
	id, _ := strconv.Atoi(idStr)
	v := models.{{ctrlName}}{Id: id}
	if err := json.Unmarshal(c.Ctx.Input.RequestBody, &v); err == nil {
		if err := models.Update{{ctrlName}}ById(&v); err == nil {
			c.Data["json"] = "OK"
		} else {
			c.Data["json"] = err.Error()
		}
	} else {
		c.Data["json"] = err.Error()
	}
	c.ServeJSON()
}

// Delete ...
// @Title Delete
// @Description delete the {{ctrlName}}
// @Param	id		path 	string	true		"The id you want to delete"
// @Success 200 {string} delete success!
// @Failure 403 id is empty
// @router /:id [delete]
func (c *{{ctrlName}}Controller) Delete() {
	idStr := c.Ctx.Input.Param(":id")
	id, _ := strconv.Atoi(idStr)
	if err := models.Delete{{ctrlName}}(id); err == nil {
		c.Data["json"] = "OK"
	} else {
		c.Data["json"] = err.Error()
	}
	c.ServeJSON()
}
`
	RouterTPL = `// @APIVersion 1.0.0
// @Title beego Test API
// @Description beego has a very cool tools to autogenerate documents for your API
// @Contact astaxie@gmail.com
// @TermsOfServiceUrl http://beego.me/
// @License Apache 2.0
// @LicenseUrl http://www.apache.org/licenses/LICENSE-2.0.html
package routers

import (
	"{{pkgPath}}/controllers"

	"github.com/astaxie/beego"
)

func init() {
	ns := beego.NewNamespace("/v1",
		{{nameSpaces}}
	)
	beego.AddNamespace(ns)
}
`
	NamespaceTPL = `
		beego.NSNamespace("/{{nameSpace}}",
			beego.NSInclude(
				&controllers.{{ctrlName}}Controller{},
			),
		),
`

	ModelsTPL = `package models

import (
	"errors"
	"strings"
	"sync"

	"github.com/jinzhu/gorm"
	_ "github.com/jinzhu/gorm/dialects/{{.Dialect}}"
)

var once sync.Once // protects the following db to be initialized once
var db *gorm.DB

func Open(dialect, connStr string, logDetail bool) (err error) {
	if db != nil {
		return errors.New("db already opened")
	}

	once.Do(func() {
		{{if eq .Dialect "mysql"}}// 对MySQL的特殊处理
		if !strings.Contains(connStr, "?") {
			connStr += "?parseTime=True"
		}
		if !strings.Contains(connStr, "parseTime") {
			connStr += "&parseTime=True"
		}
		if !strings.Contains(connStr, "loc") {
			connStr += "&loc=Local"
		}
		if !strings.Contains(connStr, "charset") {
			connStr += "&charset=utf8mb4"
		}{{end}}
		db, err = gorm.Open("{{.Dialect}}", connStr)
	})
    db.LogMode(logDetail)
	return
}

func DB() *gorm.DB {
	if db == nil {
		return nil
	}

	return db.New()
}

func Close() (err error) {
	if db != nil {
		defer func() {
			if err == nil {
				// if successfully closed, clear dangling pointer
				db = nil
			}
		}()
		return db.Close()
	}

	// omit if db is not in open
	return nil
}
`
)
