package ravendb

import "reflect"

type LoadOperation struct {
	_session *InMemoryDocumentSessionOperations

	_ids                []string
	_includes           []string
	_idsToCheckOnServer []string
}

func NewLoadOperation(_session *InMemoryDocumentSessionOperations) *LoadOperation {
	return &LoadOperation{
		_session: _session,
	}
}

func (o *LoadOperation) createRequest() *RavenCommand {
	if len(o._idsToCheckOnServer) == 0 {
		return nil
	}

	if o._session.checkIfIdAlreadyIncluded(o._ids, o._includes) {
		return nil
	}

	o._session.incrementRequestCount()

	return NewGetDocumentsCommand(o._idsToCheckOnServer, o._includes, false)
}

func (o *LoadOperation) byId(id string) *LoadOperation {
	if id == "" {
		return o
	}

	if o._ids == nil {
		o._ids = []string{id}
	}

	if o._session.IsLoadedOrDeleted(id) {
		return o
	}

	o._idsToCheckOnServer = append(o._idsToCheckOnServer, id)
	return o
}

func (o *LoadOperation) withIncludes(includes []string) *LoadOperation {
	o._includes = includes
	return o
}

func (o *LoadOperation) byIds(ids []string) *LoadOperation {
	// TODO: should this be a copy?
	o._ids = ids

	distinct := NewSet_String()
	distinct.cmp = String_compareToIgnoreCase

	for _, id := range ids {
		if id != "" {
			distinct.add(id)
		}
	}

	for _, id := range distinct.strings {
		o.byId(id)
	}

	return o
}

func (o *LoadOperation) getDocument(clazz reflect.Type) interface{} {
	return o.getDocumentWithID(clazz, o._ids[0])
}

func (o *LoadOperation) getDocumentWithID(clazz reflect.Type, id string) interface{} {
	if id == "" {
		return Defaults_defaultValue(clazz)
	}

	if o._session.IsDeleted(id) {
		return Defaults_defaultValue(clazz)
	}

	doc := o._session.documentsById.getValue(id)
	if doc != nil {
		// TODO: propagate error
		res, _ := o._session.TrackEntityInDocumentInfo(clazz, doc)
		return res
	}

	doc, _ = o._session.includedDocumentsById[id]
	if doc != nil {
		// TODO: propagate error
		res, _ := o._session.TrackEntityInDocumentInfo(clazz, doc)
		return res
	}

	return Defaults_defaultValue(clazz)
}

func (o *LoadOperation) getDocuments(clazz reflect.Type) map[string]interface{} {
	panicIf(true, "NYI")
	return nil
}

func (o *LoadOperation) setResult(result *GetDocumentsResult) {
	if result == nil {
		return
	}

	o._session.registerIncludes(result.getIncludes())

	results := result.getResults()
	for _, document := range results {
		// TODO: Java also does document.isNull()
		if document == nil {
			continue
		}
		newDocumentInfo := DocumentInfo_getNewDocumentInfo(document)
		o._session.documentsById.add(newDocumentInfo)
	}

	o._session.registerMissingIncludes(result.getResults(), result.getIncludes(), o._includes)
}
