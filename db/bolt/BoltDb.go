package bolt

import (
	"encoding/json"
	"fmt"
	"github.com/ansible-semaphore/semaphore/db"
	"github.com/ansible-semaphore/semaphore/util"
	"go.etcd.io/bbolt"
	"reflect"
	"sort"
	"strconv"
)


type enumerable interface {
	First() (key []byte, value []byte)
	Next() (key []byte, value []byte)
}


type BoltDb struct {
	db *bbolt.DB
}

func makeBucketId(props db.ObjectProperties, ids ...int) []byte {
	n := len(ids)

	id := props.TableName
	for i := 0; i < n; i++ {
		id += fmt.Sprintf("_%010d", ids[i])
	}

	return []byte(id)
}

func (d *BoltDb) Migrate() error {
	return nil
}

func (d *BoltDb) Connect() error {
	config, err := util.Config.GetDBConfig()
	if err != nil {
		return err
	}
	d.db, err = bbolt.Open(config.Hostname, 0666, nil)
	if err != nil {
		return err
	}
	return nil
}

func (d *BoltDb) Close() error {
	return d.db.Close()
}

func (d *BoltDb) getObject(projectID int, props db.ObjectProperties, objectID int, object interface{}) (err error) {
	err = d.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket(makeBucketId(props, projectID))
		if b == nil {
			return db.ErrNotFound
		}

		id := []byte(strconv.Itoa(objectID))
		str := b.Get(id)
		if str == nil {
			return db.ErrNotFound
		}

		return json.Unmarshal(str, &object)
	})

	return
}

func getFieldNameByTag(t reflect.Type, tag string, value string) (string, error) {
	n := t.NumField()
	for i := 0; i < n; i++ {
		if t.Field(i).Tag.Get(tag) == value {
			return t.Field(i).Name, nil
		}
	}
	return "", fmt.Errorf("")
}

func sortObjects(objects interface{}, sortBy string, sortInverted bool) error {
	objectsValue := reflect.ValueOf(objects).Elem()
	objType := objectsValue.Type().Elem()

	fieldName, err := getFieldNameByTag(objType, "db", sortBy)
	if err != nil {
		return err
	}

	sort.SliceStable(objectsValue.Interface(), func (i, j int) bool {
		valueI := objectsValue.Index(i).FieldByName(fieldName)
		valueJ := objectsValue.Index(j).FieldByName(fieldName)

		less := false

		switch valueI.Kind() {
		case reflect.Int:
		case reflect.Int8:
		case reflect.Int16:
		case reflect.Int32:
		case reflect.Int64:
		case reflect.Uint:
		case reflect.Uint8:
		case reflect.Uint16:
		case reflect.Uint32:
		case reflect.Uint64:
			less = valueI.Int() < valueJ.Int()
		case reflect.Float32:
		case reflect.Float64:
			less = valueI.Float() < valueJ.Float()
		case reflect.String:
			less = valueI.String() < valueJ.String()
		}

		if sortInverted {
			less = !less
		}

		return less
	})

	return nil
}

func unmarshalObjects(rawData enumerable, params db.RetrieveQueryParams, objects interface{}) (err error) {
	objectsValue := reflect.ValueOf(objects).Elem()
	objType := objectsValue.Type().Elem()

	i := 0 // current item index
	n := 0 // number of added items

	for k, v := rawData.First(); k != nil; k, v = rawData.Next() {
		if i < params.Offset {
			continue
		}

		obj := reflect.New(objType).Elem()
		err = json.Unmarshal(v, &obj)
		if err == nil {
			return err
		}

		objectsValue.Set(reflect.Append(objectsValue, obj))

		n++

		if n > params.Count {
			break
		}
	}

	if err != nil {
		return
	}

	if params.SortBy != "" {
		err = sortObjects(objects, params.SortBy, params.SortInverted)
	}

	return
}

func (d *BoltDb) getObjects(projectID int, props db.ObjectProperties, params db.RetrieveQueryParams, objects interface{}) error {
	return d.db.View(func(tx *bbolt.Tx) error {

		b := tx.Bucket(makeBucketId(props, projectID))
		c := b.Cursor()

		return unmarshalObjects(c, params, objects)
	})
}


func (d *BoltDb) isObjectInUse(projectID int, props db.ObjectProperties, objectID int) (inUse bool, err error) {
	err = d.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket(makeBucketId(props, projectID))
		inUse = b != nil && b.Get([]byte(strconv.Itoa(objectID))) != nil
		return nil
	})

	return
}

func (d *BoltDb) deleteObject(projectID int, props db.ObjectProperties, objectID int) error {
	inUse, err := d.isObjectInUse(projectID, props, objectID)

	if err != nil {
		return err
	}

	if inUse {
		return db.ErrInvalidOperation
	}

	return d.db.Update(func (tx *bbolt.Tx) error {
		b := tx.Bucket(makeBucketId(db.InventoryObject, projectID))
		if b == nil {
			return db.ErrNotFound
		}
		return b.Delete([]byte(strconv.Itoa(objectID)))
	})
}

func (d *BoltDb) deleteObjectSoft(projectID int, props db.ObjectProperties, objectID int) error {
	return d.deleteObject(projectID, props, objectID)
}

func (d *BoltDb) updateObject(projectID int, props db.ObjectProperties, object interface{}) error {
	return d.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket(makeBucketId(props, projectID))
		if b == nil {
			return db.ErrNotFound
		}

		idValue := reflect.ValueOf(object).FieldByName("ID")

		id := []byte(strconv.Itoa(int(idValue.Int())))
		if b.Get(id) == nil {
			return db.ErrNotFound
		}

		str, err := json.Marshal(object)
		if err != nil {
			return err
		}

		return b.Put(id, str)
	})
}

func (d *BoltDb) createObject(projectID int, props db.ObjectProperties, object interface{}) (interface{}, error) {
	err := d.db.Update(func(tx *bbolt.Tx) error {
		b, err2 := tx.CreateBucketIfNotExists(makeBucketId(props, projectID))
		if err2 != nil {
			return err2
		}

		id, err2 := b.NextSequence()
		if err2 != nil {
			return err2
		}

		idValue := reflect.ValueOf(object).FieldByName("ID")

		idValue.SetInt(int64(id))

		str, err2 := json.Marshal(object)
		if err2 != nil {
			return err2
		}

		return b.Put([]byte(strconv.Itoa(int(id))), str)
	})

	return object, err
}