package ravendb

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func query_querySimple(t *testing.T) {
	var err error
	store := getDocumentStoreMust(t)
	defer store.Close()

	{
		session := openSessionMust(t, store)

		user1 := NewUser()
		user1.setName("John")

		user2 := NewUser()
		user2.setName("Jane")

		user3 := NewUser()
		user3.setName("Tarzan")

		err = session.StoreEntityWithID(user1, "users/1")
		assert.NoError(t, err)
		err = session.StoreEntityWithID(user2, "users/2")
		assert.NoError(t, err)
		err = session.StoreEntityWithID(user3, "users/3")
		assert.NoError(t, err)
		err = session.SaveChanges()
		assert.NoError(t, err)

		q := session.advanced().documentQueryAll(getTypeOf(&User{}), "", "users", false)
		queryResult, err := q.toList()
		assert.NoError(t, err)
		assert.Equal(t, len(queryResult), 3)

		session.Close()
	}
}

// TODO: requires Lazy support
func query_queryLazily(t *testing.T) {}

func query_collectionsStats(t *testing.T) {
	var err error
	store := getDocumentStoreMust(t)
	defer store.Close()

	{
		session := openSessionMust(t, store)
		user1 := NewUser()
		user1.setName("John")

		user2 := NewUser()
		user2.setName("Jane")

		err = session.StoreEntityWithID(user1, "users/1")
		assert.NoError(t, err)
		err = session.StoreEntityWithID(user2, "users/2")
		assert.NoError(t, err)
		err = session.SaveChanges()
		assert.NoError(t, err)

		session.Close()
	}

	op := NewGetCollectionStatisticsOperation()
	err = store.maintenance().send(op)
	assert.NoError(t, err)
	stats := op.Command.Result
	assert.Equal(t, stats.getCountOfDocuments(), 2)
	coll := stats.getCollections()["Users"]
	assert.Equal(t, coll, 2)
}

func query_queryWithWhereClause(t *testing.T) {

	var err error
	store := getDocumentStoreMust(t)
	defer store.Close()

	{
		session := openSessionMust(t, store)

		user1 := NewUser()
		user1.setName("John")

		user2 := NewUser()
		user2.setName("Jane")

		user3 := NewUser()
		user3.setName("Tarzan")

		err = session.StoreEntityWithID(user1, "users/1")
		assert.NoError(t, err)
		err = session.StoreEntityWithID(user2, "users/2")
		assert.NoError(t, err)
		err = session.StoreEntityWithID(user3, "users/3")
		assert.NoError(t, err)
		err = session.SaveChanges()
		assert.NoError(t, err)

		q := session.queryWithQuery(getTypeOf(&User{}), Query_collection("users"))
		q = q.whereStartsWith("name", "J")
		queryResult, err := q.toList()
		assert.NoError(t, err)

		q2 := session.queryWithQuery(getTypeOf(&User{}), Query_collection("users"))
		q2 = q2.whereEquals("name", "Tarzan")
		queryResult2, err := q2.toList()
		assert.NoError(t, err)

		q3 := session.queryWithQuery(getTypeOf(&User{}), Query_collection("users"))
		q3 = q3.whereEndsWith("name", "n")
		queryResult3, err := q3.toList()
		assert.NoError(t, err)

		assert.Equal(t, len(queryResult), 2)
		assert.Equal(t, len(queryResult2), 1)
		assert.Equal(t, len(queryResult3), 2)

		session.Close()
	}
}

func query_queryMapReduceWithCount(t *testing.T)          {}
func query_queryMapReduceWithSum(t *testing.T)            {}
func query_queryMapReduceIndex(t *testing.T)              {}
func query_querySingleProperty(t *testing.T)              {}
func query_queryWithSelect(t *testing.T)                  {}
func query_queryWithWhereIn(t *testing.T)                 {}
func query_queryWithWhereBetween(t *testing.T)            {}
func query_queryWithWhereLessThan(t *testing.T)           {}
func query_queryWithWhereLessThanOrEqual(t *testing.T)    {}
func query_queryWithWhereGreaterThan(t *testing.T)        {}
func query_queryWithWhereGreaterThanOrEqual(t *testing.T) {}
func query_queryWithProjection(t *testing.T)              {}
func query_queryWithProjection2(t *testing.T)             {}
func query_queryDistinct(t *testing.T)                    {}
func query_querySearchWithOr(t *testing.T)                {}
func query_queryNoTracking(t *testing.T)                  {}
func query_querySkipTake(t *testing.T)                    {}
func query_rawQuerySkipTake(t *testing.T)                 {}
func query_parametersInRawQuery(t *testing.T)             {}
func query_queryLucene(t *testing.T)                      {}
func query_queryWhereExact(t *testing.T)                  {}
func query_queryWhereNot(t *testing.T)                    {}
func query_queryWithDuration(t *testing.T)                {}
func query_queryFirst(t *testing.T)                       {}
func query_queryParameters(t *testing.T)                  {}
func query_queryRandomOrder(t *testing.T)                 {}

func query_queryWhereExists(t *testing.T) {
	store := getDocumentStoreMust(t)
	defer store.Close()

	query_addUsers(t, store)
	{
		session := openSessionMust(t, store)

		{
			q := session.query(getTypeOf(&User{}))
			q = q.whereExists("name")
			res, err := q.toList()
			assert.NoError(t, err)
			assert.Equal(t, len(res), 3)
		}

		{
			q := session.query(getTypeOf(&User{}))
			q = q.whereExists("name")
			q = q.andAlso()
			q = q.not()
			q = q.whereExists("no_such_field")
			res, err := q.toList()
			assert.NoError(t, err)
			assert.Equal(t, len(res), 3)
		}

		session.Close()
	}
}

func query_queryWithBoost(t *testing.T) {}

func makeUsersByName() *AbstractIndexCreationTask {
	res := NewAbstractIndexCreationTask("UsersByName")
	res.smap = "from c in docs.Users select new " +
		" {" +
		"    c.name, " +
		"    count = 1" +
		"}"
	res.reduce = "from result in results " +
		"group result by result.name " +
		"into g " +
		"select new " +
		"{ " +
		"  name = g.Key, " +
		"  count = g.Sum(x => x.count) " +
		"}"
	return res
}

func query_addUsers(t *testing.T, store *IDocumentStore) {
	var err error

	{
		session := openSessionMust(t, store)
		user1 := NewUser()
		user1.setName("John")
		user1.setAge(3)

		user2 := NewUser()
		user2.setName("John")
		user2.setAge(5)

		user3 := NewUser()
		user3.setName("Tarzan")
		user3.setAge(2)

		err = session.StoreEntityWithID(user1, "users/1")
		assert.NoError(t, err)
		err = session.StoreEntityWithID(user2, "users/2")
		assert.NoError(t, err)
		err = session.StoreEntityWithID(user3, "users/3")
		assert.NoError(t, err)
		err = session.SaveChanges()
		assert.NoError(t, err)

		session.Close()
	}

	err = store.executeIndex(makeUsersByName())
	assert.NoError(t, err)
	err = gRavenTestDriver.waitForIndexing(store, "", 0)
	assert.NoError(t, err)
}

func query_queryWithCustomize(t *testing.T) {}
func query_queryLongRequest(t *testing.T)   {}
func query_queryByIndex(t *testing.T)       {}

func TestQuery(t *testing.T) {
	if dbTestsDisabled() {
		return
	}

	if false {
		oldDumpFailedHTTP := dumpFailedHTTP
		dumpFailedHTTP = true
		defer func() {
			dumpFailedHTTP = oldDumpFailedHTTP
		}()
	}

	destroyDriver := createTestDriver(t)
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("Recovered in %s\n", t.Name())
		}
		destroyDriver()
	}()

	// matches order of Java tests
	query_queryWhereExists(t)
	query_querySearchWithOr(t)
	query_rawQuerySkipTake(t)
	query_queryWithDuration(t)
	query_queryWithWhereClause(t)
	query_queryMapReduceIndex(t)
	query_queryLazily(t)
	query_queryLucene(t)
	query_queryWithWhereGreaterThan(t)
	query_querySimple(t)
	query_queryWithSelect(t)
	query_collectionsStats(t)
	query_queryWithWhereBetween(t)
	query_queryRandomOrder(t)
	query_queryNoTracking(t)
	query_queryLongRequest(t)
	query_queryWithProjection2(t)
	query_queryWhereNot(t)
	query_querySkipTake(t)
	query_queryWithProjection(t)
	query_queryFirst(t)
	query_querySingleProperty(t)
	query_parametersInRawQuery(t)
	query_queryWithWhereLessThan(t)
	query_queryMapReduceWithCount(t)
	query_queryWithWhereGreaterThanOrEqual(t)
	query_queryWithCustomize(t)
	query_queryWithBoost(t)
	query_queryMapReduceWithSum(t)
	query_queryWhereExact(t)
	query_queryParameters(t)
	query_queryByIndex(t)
	query_queryWithWhereIn(t)
	query_queryDistinct(t)
	query_queryWithWhereLessThanOrEqual(t)
}
