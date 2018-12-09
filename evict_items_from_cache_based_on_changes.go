package ravendb

type EvictItemsFromCacheBasedOnChanges struct {
	_databaseName          string
	_changes               IDatabaseChanges
	_documentsSubscription CleanCloseable
	_indexesSubscription   CleanCloseable
	_requestExecutor       *RequestExecutor
}

func NewEvictItemsFromCacheBasedOnChanges(store *DocumentStore, databaseName string) *EvictItemsFromCacheBasedOnChanges {
	res := &EvictItemsFromCacheBasedOnChanges{
		_databaseName:    databaseName,
		_changes:         store.ChangesWithDatabaseName(databaseName),
		_requestExecutor: store.GetRequestExecutorWithDatabase(databaseName),
	}
	docSub, err := res._changes.ForAllDocuments()
	must(err) // TOOD: return an error?
	res._documentsSubscription = docSub.Subscribe(res)
	indexSub, err := res._changes.ForAllIndexes()
	must(err) // TOOD: return an error?
	res._indexesSubscription = indexSub.Subscribe(res)
	return res
}

func (e *EvictItemsFromCacheBasedOnChanges) OnNext(value interface{}) {
	if documentChange, ok := value.(*DocumentChange); ok {
		tp := documentChange.Type
		if tp == DocumentChangeTypes_PUT || tp == DocumentChangeTypes_DELETE {
			cache := e._requestExecutor.GetCache()
			cache.generation.incrementAndGet()
		}
	} else if indexChange, ok := value.(*IndexChange); ok {
		tp := indexChange.Type
		if tp == IndexChangeTypes_BATCH_COMPLETED || tp == IndexChangeTypes_INDEX_REMOVED {
			e._requestExecutor.GetCache().generation.incrementAndGet()
		}
	}
}

func (e *EvictItemsFromCacheBasedOnChanges) OnError(err error) {
	// empty
}

func (e *EvictItemsFromCacheBasedOnChanges) OnCompleted() {
	// empty
}

func (e *EvictItemsFromCacheBasedOnChanges) Close() {
	changesScope := e._changes
	defer changesScope.Close()

	e._documentsSubscription.Close()
	e._indexesSubscription.Close()
}
