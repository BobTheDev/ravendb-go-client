package ravendb

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Note: Java's IAbstractDocumentQuery is AbstractDocumentQuery

// AbstractDocumentQuery is a base class for describing a query
type AbstractDocumentQuery struct {
	clazz                   reflect.Type
	aliasToGroupByFieldName map[string]string
	defaultOperator         QueryOperator

	// Note: rootTypes is not used in Go because we only have one ID property

	negate             bool
	indexName          string
	collectionName     string
	currentClauseDepth int
	queryRaw           string
	queryParameters    Parameters

	isIntersect bool
	isGroupBy   bool

	theSession *InMemoryDocumentSessionOperations

	pageSize *int

	selectTokens       []queryToken
	fromToken          *fromToken
	declareToken       *declareToken
	loadTokens         []*loadToken
	fieldsToFetchToken *fieldsToFetchToken

	whereTokens   []queryToken
	groupByTokens []queryToken
	orderByTokens []queryToken

	start       int
	conventions *DocumentConventions

	timeout time.Duration

	theWaitForNonStaleResults bool

	includes []string

	queryStats *QueryStatistics

	disableEntitiesTracking bool

	disableCaching bool

	isInMoreLikeThis bool

	// Go doesn't allow comparing functions so to remove we use index returned
	// by add() function. We maintain stable index by never shrinking
	// callback arrays. We assume there is no high churn of adding/removing
	// callbacks
	beforeQueryExecutedCallback []func(*IndexQuery)
	afterQueryExecutedCallback  []func(*QueryResult)
	afterStreamExecutedCallback []func(map[string]interface{})

	queryOperation *QueryOperation

	// SelectFields logic has to be delayed until ToList
	// because only then we know the type of the result
	selectFieldsArgs *QueryData
}

func (q *AbstractDocumentQuery) isDistinct() bool {
	if len(q.selectTokens) == 0 {
		return false
	}
	_, ok := q.selectTokens[0].(*distinctToken)
	return ok
}

func (q *AbstractDocumentQuery) getConventions() *DocumentConventions {
	return q.conventions
}

func (q *AbstractDocumentQuery) getSession() *InMemoryDocumentSessionOperations {
	return q.theSession
}

func (q *AbstractDocumentQuery) isDynamicMapReduce() bool {
	return len(q.groupByTokens) > 0
}

func getQueryDefaultTimeout() time.Duration {
	return time.Second * 15
}

func NewAbstractDocumentQueryOld(clazz reflect.Type, session *InMemoryDocumentSessionOperations, indexName string, collectionName string, isGroupBy bool, declareToken *declareToken, loadTokens []*loadToken, fromAlias string) *AbstractDocumentQuery {
	res := &AbstractDocumentQuery{
		clazz:                   clazz,
		defaultOperator:         QueryOperatorAnd,
		isGroupBy:               isGroupBy,
		indexName:               indexName,
		collectionName:          collectionName,
		declareToken:            declareToken,
		loadTokens:              loadTokens,
		theSession:              session,
		aliasToGroupByFieldName: make(map[string]string),
		queryParameters:         make(map[string]interface{}),
		queryStats:              NewQueryStatistics(),
	}
	res.fromToken = createFromToken(indexName, collectionName, fromAlias)
	f := func(queryResult *QueryResult) {
		res.updateStatsAndHighlightings(queryResult)
	}
	res.addAfterQueryExecutedListener(f)
	if session == nil {
		res.conventions = NewDocumentConventions()
	} else {
		res.conventions = session.GetConventions()
	}
	return res
}

// NewAbstractDocumentQuery returns new AbstractDocumentQuery
func NewAbstractDocumentQuery(session *InMemoryDocumentSessionOperations, indexName string, collectionName string, isGroupBy bool, declareToken *declareToken, loadTokens []*loadToken, fromAlias string) *AbstractDocumentQuery {
	res := &AbstractDocumentQuery{
		defaultOperator:         QueryOperatorAnd,
		isGroupBy:               isGroupBy,
		indexName:               indexName,
		collectionName:          collectionName,
		declareToken:            declareToken,
		loadTokens:              loadTokens,
		theSession:              session,
		aliasToGroupByFieldName: make(map[string]string),
		queryParameters:         make(map[string]interface{}),
		queryStats:              NewQueryStatistics(),
	}
	// if those are not provided, we delay creating fromToken
	// until ToList()
	if indexName != "" || collectionName != "" || fromAlias != "" {
		res.fromToken = createFromToken(indexName, collectionName, fromAlias)
	}
	f := func(queryResult *QueryResult) {
		res.updateStatsAndHighlightings(queryResult)
	}
	res.addAfterQueryExecutedListener(f)
	if session == nil {
		res.conventions = NewDocumentConventions()
	} else {
		res.conventions = session.GetConventions()
	}
	return res
}

func (q *AbstractDocumentQuery) usingDefaultOperator(operator QueryOperator) {
	if len(q.whereTokens) > 0 {
		//throw new IllegalStateError("Default operator can only be set before any where clause is added.");
		panicIf(true, "Default operator can only be set before any where clause is added.")
	}

	q.defaultOperator = operator
}

func (q *AbstractDocumentQuery) waitForNonStaleResults(waitTimeout time.Duration) {
	q.theWaitForNonStaleResults = true
	if waitTimeout == 0 {
		waitTimeout = getQueryDefaultTimeout()
	}
	q.timeout = waitTimeout
}

func (q *AbstractDocumentQuery) initializeQueryOperation() (*QueryOperation, error) {
	indexQuery := q.GetIndexQuery()

	return NewQueryOperation(q.theSession, q.indexName, indexQuery, q.fieldsToFetchToken, q.disableEntitiesTracking, false, false)
}

func (q *AbstractDocumentQuery) GetIndexQuery() *IndexQuery {
	query := q.String()
	indexQuery := q.GenerateIndexQuery(query)
	q.invokeBeforeQueryExecuted(indexQuery)
	return indexQuery
}

func (q *AbstractDocumentQuery) getProjectionFields() []string {

	if q.fieldsToFetchToken != nil && q.fieldsToFetchToken.projections != nil {
		return q.fieldsToFetchToken.projections
	}
	return nil
}

func (q *AbstractDocumentQuery) randomOrdering() {
	q.assertNoRawQuery()

	q.noCaching()
	q.orderByTokens = append(q.orderByTokens, orderByTokenRandom)
}

func (q *AbstractDocumentQuery) randomOrderingWithSeed(seed string) {
	q.assertNoRawQuery()

	if stringIsBlank(seed) {
		q.randomOrdering()
		return
	}

	q.noCaching()
	q.orderByTokens = append(q.orderByTokens, orderByTokenCreateRandom(seed))
}

func (q *AbstractDocumentQuery) AddGroupByAlias(fieldName string, projectedName string) {
	q.aliasToGroupByFieldName[projectedName] = fieldName
}

func (q *AbstractDocumentQuery) assertNoRawQuery() {
	panicIf(q.queryRaw != "", "RawQuery was called, cannot modify this query by calling on operations that would modify the query (such as Where, Select, OrderBy, GroupBy, etc)")
}

func (q *AbstractDocumentQuery) addParameter(name string, value interface{}) {
	name = strings.TrimPrefix(name, "$")
	if _, ok := q.queryParameters[name]; ok {
		// throw new IllegalStateError("The parameter " + name + " was already added");
		panicIf(true, "The parameter "+name+" was already added")
	}

	q.queryParameters[name] = value
}

func (q *AbstractDocumentQuery) groupBy(fieldName string, fieldNames ...string) {
	var mapping []*GroupBy
	for _, x := range fieldNames {
		el := NewGroupByField(x)
		mapping = append(mapping, el)
	}
	q.groupBy2(NewGroupByField(fieldName), mapping...)
}

// TODO: better name
func (q *AbstractDocumentQuery) groupBy2(field *GroupBy, fields ...*GroupBy) {
	// TODO: if q.fromToken is nil, needs to do this check in ToList()
	if q.fromToken != nil && !q.fromToken.isDynamic {
		//throw new IllegalStateError("groupBy only works with dynamic queries");
		panicIf(true, "groupBy only works with dynamic queries")
	}

	q.assertNoRawQuery()
	q.isGroupBy = true

	fieldName := q.ensureValidFieldName(field.Field, false)

	q.groupByTokens = append(q.groupByTokens, createGroupByTokenWithMethod(fieldName, field.Method))

	if len(fields) == 0 {
		return
	}

	for _, item := range fields {
		fieldName = q.ensureValidFieldName(item.Field, false)
		q.groupByTokens = append(q.groupByTokens, createGroupByTokenWithMethod(fieldName, item.Method))
	}
}

func (q *AbstractDocumentQuery) groupByKey(fieldName string, projectedName string) {
	q.assertNoRawQuery()
	q.isGroupBy = true

	_, hasProjectedName := q.aliasToGroupByFieldName[projectedName]
	_, hasFieldName := q.aliasToGroupByFieldName[fieldName]

	if projectedName != "" && hasProjectedName {
		aliasedFieldName := q.aliasToGroupByFieldName[projectedName]
		if fieldName == "" || strings.EqualFold(fieldName, projectedName) {
			fieldName = aliasedFieldName
		}
	} else if fieldName != "" && hasFieldName {
		aliasedFieldName := q.aliasToGroupByFieldName[fieldName]
		fieldName = aliasedFieldName
	}

	q.selectTokens = append(q.selectTokens, createGroupByKeyToken(fieldName, projectedName))
}

// projectedName is optional
func (q *AbstractDocumentQuery) groupBySum(fieldName string, projectedName string) {
	q.assertNoRawQuery()
	q.isGroupBy = true

	fieldName = q.ensureValidFieldName(fieldName, false)
	q.selectTokens = append(q.selectTokens, createGroupBySumToken(fieldName, projectedName))
}

// projectedName is optional
func (q *AbstractDocumentQuery) groupByCount(projectedName string) {
	q.assertNoRawQuery()
	q.isGroupBy = true

	t := &groupByCountToken{
		fieldName: projectedName,
	}
	q.selectTokens = append(q.selectTokens, t)
}

func (q *AbstractDocumentQuery) whereTrue() {
	tokensRef := q.getCurrentWhereTokensRef()
	q.appendOperatorIfNeeded(tokensRef)
	q.negateIfNeeded(tokensRef, "")

	tokens := *tokensRef
	tokens = append(tokens, trueTokenInstance)
	*tokensRef = tokens
}

func (q *AbstractDocumentQuery) moreLikeThis() *MoreLikeThisScope {
	q.appendOperatorIfNeeded(&q.whereTokens)

	token := newMoreLikeThisToken()
	q.whereTokens = append(q.whereTokens, token)

	q.isInMoreLikeThis = true
	add := func(o interface{}) string {
		return q.addQueryParameter(o)
	}
	onDispose := func() {
		q.isInMoreLikeThis = false
	}
	return NewMoreLikeThisScope(token, add, onDispose)
}

func (q *AbstractDocumentQuery) include(path string) {
	q.includes = append(q.includes, path)
}

// TODO: see if count can be int
func (q *AbstractDocumentQuery) take(count *int) {
	q.pageSize = count
}

func (q *AbstractDocumentQuery) skip(count int) {
	q.start = count
}

func (q *AbstractDocumentQuery) whereLucene(fieldName string, whereClause string) {
	fieldName = q.ensureValidFieldName(fieldName, false)

	tokensRef := q.getCurrentWhereTokensRef()
	tokens := *tokensRef
	q.appendOperatorIfNeeded(tokensRef)
	q.negateIfNeeded(tokensRef, fieldName)

	whereToken := createWhereTokenWithOptions(WhereOperatorLucene, fieldName, q.addQueryParameter(whereClause), nil)
	tokens = append(tokens, whereToken)
	*tokensRef = tokens
}

func (q *AbstractDocumentQuery) openSubclause() {
	q.currentClauseDepth++

	tokensRef := q.getCurrentWhereTokensRef()
	q.appendOperatorIfNeeded(tokensRef)
	q.negateIfNeeded(tokensRef, "")

	tokens := *tokensRef
	tokens = append(tokens, openSubclauseTokenInstance)
	*tokensRef = tokens
}

func (q *AbstractDocumentQuery) closeSubclause() {
	q.currentClauseDepth--

	tokensRef := q.getCurrentWhereTokensRef()
	tokens := *tokensRef
	tokens = append(tokens, closeSubclauseTokenInstance)
	*tokensRef = tokens
}

func (q *AbstractDocumentQuery) whereEquals(fieldName string, value interface{}) {
	params := &whereParams{
		fieldName: fieldName,
		value:     value,
	}
	q.whereEqualsWithParams(params)
}

func (q *AbstractDocumentQuery) whereEqualsWithMethodCall(fieldName string, method MethodCall) {
	q.whereEquals(fieldName, method)
}

func (q *AbstractDocumentQuery) whereEqualsWithParams(whereParams *whereParams) {
	if q.negate {
		q.negate = false
		q.whereNotEqualsWithParams(whereParams)
		return
	}

	whereParams.fieldName = q.ensureValidFieldName(whereParams.fieldName, whereParams.isNestedPath)

	tokensRef := q.getCurrentWhereTokensRef()
	q.appendOperatorIfNeeded(tokensRef)

	if q.ifValueIsMethod(WhereOperatorEquals, whereParams, tokensRef) {
		return
	}

	transformToEqualValue := q.transformValue(whereParams)
	addQueryParameter := q.addQueryParameter(transformToEqualValue)
	whereToken := createWhereTokenWithOptions(WhereOperatorEquals, whereParams.fieldName, addQueryParameter, NewWhereOptionsWithExact(whereParams.isExact))

	tokens := *tokensRef
	tokens = append(tokens, whereToken)
	*tokensRef = tokens
}

func (q *AbstractDocumentQuery) ifValueIsMethod(op WhereOperator, whereParams *whereParams, tokensRef *[]queryToken) bool {
	if mc, ok := whereParams.value.(*CmpXchg); ok {
		n := len(mc.args)
		args := make([]string, n)
		for i := 0; i < n; i++ {
			args[i] = q.addQueryParameter(mc.args[i])
		}

		opts := NewWhereOptionsWithMethod(MethodsTypeCmpXChg, args, mc.accessPath, whereParams.isExact)
		token := createWhereTokenWithOptions(op, whereParams.fieldName, "", opts)

		tokens := *tokensRef
		tokens = append(tokens, token)
		*tokensRef = tokens
		return true
	}

	// add more if there are more types that "derive" from MethodCall
	// (by embedding MethodCallData)

	return false
}

func (q *AbstractDocumentQuery) whereNotEquals(fieldName string, value interface{}) {
	params := &whereParams{
		fieldName: fieldName,
		value:     value,
	}

	q.whereNotEqualsWithParams(params)
}

func (q *AbstractDocumentQuery) whereNotEqualsWithMethod(fieldName string, method MethodCall) {
	q.whereNotEquals(fieldName, method)
}

func (q *AbstractDocumentQuery) whereNotEqualsWithParams(whereParams *whereParams) {
	if q.negate {
		q.negate = false
		q.whereEqualsWithParams(whereParams)
		return
	}

	transformToEqualValue := q.transformValue(whereParams)

	tokensRef := q.getCurrentWhereTokensRef()
	q.appendOperatorIfNeeded(tokensRef)

	whereParams.fieldName = q.ensureValidFieldName(whereParams.fieldName, whereParams.isNestedPath)

	if q.ifValueIsMethod(WhereOperatorNotEquals, whereParams, tokensRef) {
		return
	}

	whereToken := createWhereTokenWithOptions(WhereOperatorNotEquals, whereParams.fieldName, q.addQueryParameter(transformToEqualValue), NewWhereOptionsWithExact(whereParams.isExact))
	tokens := *tokensRef
	tokens = append(tokens, whereToken)
	*tokensRef = tokens
}

func (q *AbstractDocumentQuery) NegateNext() {
	q.negate = !q.negate
}

// mark last created token as exact. only applies to select number of tokens.
// it allows fluid APIs like .Where().Exact()
// will panic if last token wasn't of compatible type as that is considered
// invalid use of API and returning an error would break fluid API
func (q *AbstractDocumentQuery) markLastTokenExact() {
	tokensRef := q.getCurrentWhereTokensRef()
	tokens := *tokensRef
	n := len(tokens)
	lastToken := tokens[n-1]
	switch tok := lastToken.(type) {
	case *whereToken:
		if tok.options == nil {
			tok.options = NewWhereOptionsWithExact(true)
		} else {
			tok.options.exact = true
		}
	default:
		panicIf(true, "expected whereToken, got %T", lastToken)
	}

	*tokensRef = tokens
}

func (q *AbstractDocumentQuery) whereIn(fieldName string, values []interface{}) {
	fieldName = q.ensureValidFieldName(fieldName, false)

	tokensRef := q.getCurrentWhereTokensRef()
	q.appendOperatorIfNeeded(tokensRef)
	q.negateIfNeeded(tokensRef, fieldName)

	whereToken := createWhereToken(WhereOperatorIn, fieldName, q.addQueryParameter(q.transformCollection(fieldName, abstractDocumentQueryUnpackCollection(values))))

	tokens := *tokensRef
	tokens = append(tokens, whereToken)
	*tokensRef = tokens
}

func (q *AbstractDocumentQuery) whereStartsWith(fieldName string, value interface{}) {
	whereParams := &whereParams{
		fieldName:      fieldName,
		value:          value,
		allowWildcards: true,
	}

	transformToEqualValue := q.transformValue(whereParams)

	tokensRef := q.getCurrentWhereTokensRef()
	q.appendOperatorIfNeeded(tokensRef)

	whereParams.fieldName = q.ensureValidFieldName(whereParams.fieldName, whereParams.isNestedPath)
	q.negateIfNeeded(tokensRef, whereParams.fieldName)

	whereToken := createWhereToken(WhereOperatorStartsWith, whereParams.fieldName, q.addQueryParameter(transformToEqualValue))

	tokens := *tokensRef
	tokens = append(tokens, whereToken)
	*tokensRef = tokens
}

func (q *AbstractDocumentQuery) whereEndsWith(fieldName string, value interface{}) {
	whereParams := &whereParams{
		fieldName:      fieldName,
		value:          value,
		allowWildcards: true,
	}

	transformToEqualValue := q.transformValue(whereParams)

	tokensRef := q.getCurrentWhereTokensRef()
	q.appendOperatorIfNeeded(tokensRef)

	whereParams.fieldName = q.ensureValidFieldName(whereParams.fieldName, whereParams.isNestedPath)
	q.negateIfNeeded(tokensRef, whereParams.fieldName)

	whereToken := createWhereToken(WhereOperatorEndsWith, whereParams.fieldName, q.addQueryParameter(transformToEqualValue))

	tokens := *tokensRef
	tokens = append(tokens, whereToken)
	*tokensRef = tokens
}

func (q *AbstractDocumentQuery) whereBetween(fieldName string, start interface{}, end interface{}) {
	fieldName = q.ensureValidFieldName(fieldName, false)

	tokensRef := q.getCurrentWhereTokensRef()
	q.appendOperatorIfNeeded(tokensRef)
	q.negateIfNeeded(tokensRef, fieldName)

	startParams := &whereParams{
		value:     start,
		fieldName: fieldName,
	}

	endParams := &whereParams{
		value:     end,
		fieldName: fieldName,
	}

	fromParam := interface{}("*")
	if start != nil {
		fromParam = q.transformValueWithRange(startParams, true)
	}
	fromParameterName := q.addQueryParameter(fromParam)

	toParam := interface{}("NULL")
	// TODO: should this be end == nil? A bug in Java code?
	if start != nil {
		toParam = q.transformValueWithRange(endParams, true)
	}
	toParameterName := q.addQueryParameter(toParam)

	whereToken := createWhereTokenWithOptions(WhereOperatorBetween, fieldName, "", NewWhereOptionsWithFromTo(false, fromParameterName, toParameterName))

	tokens := *tokensRef
	tokens = append(tokens, whereToken)
	*tokensRef = tokens
}

func (q *AbstractDocumentQuery) whereGreaterThan(fieldName string, value interface{}) {
	fieldName = q.ensureValidFieldName(fieldName, false)

	tokensRef := q.getCurrentWhereTokensRef()
	q.appendOperatorIfNeeded(tokensRef)
	q.negateIfNeeded(tokensRef, fieldName)

	whereParams := &whereParams{
		value:     value,
		fieldName: fieldName,
	}

	paramValue := interface{}("*")
	if value != nil {
		paramValue = q.transformValueWithRange(whereParams, true)
	}
	parameter := q.addQueryParameter(paramValue)

	whereToken := createWhereTokenWithOptions(WhereOperatorGreaterThan, fieldName, parameter, nil)

	tokens := *tokensRef
	tokens = append(tokens, whereToken)
	*tokensRef = tokens
}

func (q *AbstractDocumentQuery) whereGreaterThanOrEqual(fieldName string, value interface{}) {
	fieldName = q.ensureValidFieldName(fieldName, false)

	tokensRef := q.getCurrentWhereTokensRef()
	q.appendOperatorIfNeeded(tokensRef)
	q.negateIfNeeded(tokensRef, fieldName)

	whereParams := &whereParams{
		value:     value,
		fieldName: fieldName,
	}

	paramValue := interface{}("*")
	if value != nil {
		paramValue = q.transformValueWithRange(whereParams, true)
	}

	parameter := q.addQueryParameter(paramValue)

	whereToken := createWhereTokenWithOptions(WhereOperatorGreaterThanOrEqual, fieldName, parameter, nil)

	tokens := *tokensRef
	tokens = append(tokens, whereToken)
	*tokensRef = tokens
}

func (q *AbstractDocumentQuery) whereLessThan(fieldName string, value interface{}) {
	fieldName = q.ensureValidFieldName(fieldName, false)

	tokensRef := q.getCurrentWhereTokensRef()
	q.appendOperatorIfNeeded(tokensRef)
	q.negateIfNeeded(tokensRef, fieldName)

	whereParams := &whereParams{
		value:     value,
		fieldName: fieldName,
	}

	paramValue := interface{}("NULL")
	if value != nil {
		paramValue = q.transformValueWithRange(whereParams, true)
	}
	parameter := q.addQueryParameter(paramValue)
	whereToken := createWhereTokenWithOptions(WhereOperatorLessThan, fieldName, parameter, nil)

	tokens := *tokensRef
	tokens = append(tokens, whereToken)
	*tokensRef = tokens
}

func (q *AbstractDocumentQuery) whereLessThanOrEqual(fieldName string, value interface{}) {
	tokensRef := q.getCurrentWhereTokensRef()
	q.appendOperatorIfNeeded(tokensRef)
	q.negateIfNeeded(tokensRef, fieldName)

	whereParams := &whereParams{
		value:     value,
		fieldName: fieldName,
	}

	paramValue := interface{}("NULL")
	if value != nil {
		paramValue = q.transformValueWithRange(whereParams, true)
	}
	parameter := q.addQueryParameter(paramValue)
	whereToken := createWhereTokenWithOptions(WhereOperatorLessThanOrEqual, fieldName, parameter, nil)

	tokens := *tokensRef
	tokens = append(tokens, whereToken)
	*tokensRef = tokens
}

func (q *AbstractDocumentQuery) whereRegex(fieldName string, pattern string) {
	tokensRef := q.getCurrentWhereTokensRef()
	q.appendOperatorIfNeeded(tokensRef)
	q.negateIfNeeded(tokensRef, fieldName)

	whereParams := &whereParams{
		value:     pattern,
		fieldName: fieldName,
	}

	parameter := q.addQueryParameter(q.transformValue(whereParams))

	whereToken := createWhereToken(WhereOperatorRegex, fieldName, parameter)

	tokens := *tokensRef
	tokens = append(tokens, whereToken)
	*tokensRef = tokens
}

func (q *AbstractDocumentQuery) andAlso() {
	tokensRef := q.getCurrentWhereTokensRef()
	tokens := *tokensRef

	n := len(tokens)
	if n == 0 {
		return
	}

	lastToken := tokens[n-1]
	if _, ok := lastToken.(*queryOperatorToken); ok {
		//throw new IllegalStateError("Cannot add AND, previous token was already an operator token.");
		panicIf(true, "Cannot add AND, previous token was already an operator token.")
	}

	tokens = append(tokens, queryOperatorTokenAnd)
	*tokensRef = tokens
}

func (q *AbstractDocumentQuery) orElse() {
	tokensRef := q.getCurrentWhereTokensRef()
	tokens := *tokensRef
	n := len(tokens)
	if n == 0 {
		return
	}

	lastToken := tokens[n-1]
	if _, ok := lastToken.(*queryOperatorToken); ok {
		//throw new IllegalStateError("Cannot add OR, previous token was already an operator token.");
		panicIf(true, "Cannot add OR, previous token was already an operator token.")
	}

	tokens = append(tokens, queryOperatorTokenOr)
	*tokensRef = tokens
}

func (q *AbstractDocumentQuery) boost(boost float64) {
	if boost == 1.0 {
		return
	}

	tokens := q.getCurrentWhereTokens()
	n := len(tokens)
	if n == 0 {
		//throw new IllegalStateError("Missing where clause");
		panicIf(true, "Missing where clause")
	}

	maybeWhereToken := tokens[n-1]
	whereToken, ok := maybeWhereToken.(*whereToken)
	if !ok {
		//throw new IllegalStateError("Missing where clause");
		panicIf(true, "Missing where clause")
	}

	if boost <= 0.0 {
		//throw new IllegalArgumentError("Boost factor must be a positive number");
		panicIf(true, "Boost factor must be a positive number")
	}

	whereToken.options.boost = boost
}

func (q *AbstractDocumentQuery) fuzzy(fuzzy float64) {
	tokens := q.getCurrentWhereTokens()
	n := len(tokens)
	if n == 0 {
		//throw new IllegalStateError("Missing where clause");
		panicIf(true, "Missing where clause")
	}

	maybeWhereToken := tokens[n-1]
	whereToken, ok := maybeWhereToken.(*whereToken)
	if !ok {
		//throw new IllegalStateError("Missing where clause");
		panicIf(true, "Missing where clause")
	}

	if fuzzy < 0.0 || fuzzy > 1.0 {
		//throw new IllegalArgumentError("Fuzzy distance must be between 0.0 and 1.0");
		panicIf(true, "Fuzzy distance must be between 0.0 and 1.0")
	}

	whereToken.options.fuzzy = fuzzy
}

func (q *AbstractDocumentQuery) proximity(proximity int) {
	tokens := q.getCurrentWhereTokens()

	n := len(tokens)
	if n == 0 {
		//throw new IllegalStateError("Missing where clause");
		panicIf(true, "Missing where clause")
	}

	maybeWhereToken := tokens[n-1]
	whereToken, ok := maybeWhereToken.(*whereToken)
	if !ok {
		//throw new IllegalStateError("Missing where clause");
		panicIf(true, "Missing where clause")
	}

	if proximity < 1 {
		//throw new IllegalArgumentError("Proximity distance must be a positive number");
		panicIf(true, "Proximity distance must be a positive number")
	}

	whereToken.options.proximity = proximity
}

func (q *AbstractDocumentQuery) orderBy(field string) {
	q.orderByWithOrdering(field, OrderingTypeString)
}

func (q *AbstractDocumentQuery) orderByWithOrdering(field string, ordering OrderingType) {
	q.assertNoRawQuery()
	f := q.ensureValidFieldName(field, false)
	q.orderByTokens = append(q.orderByTokens, orderByTokenCreateAscending(f, ordering))
}

func (q *AbstractDocumentQuery) orderByDescending(field string) {
	q.orderByDescendingWithOrdering(field, OrderingTypeString)
}

func (q *AbstractDocumentQuery) orderByDescendingWithOrdering(field string, ordering OrderingType) {
	q.assertNoRawQuery()
	f := q.ensureValidFieldName(field, false)
	q.orderByTokens = append(q.orderByTokens, orderByTokenCreateDescending(f, ordering))
}

func (q *AbstractDocumentQuery) orderByScore() {
	q.assertNoRawQuery()

	q.orderByTokens = append(q.orderByTokens, orderByTokenScoreAscending)
}

func (q *AbstractDocumentQuery) orderByScoreDescending() {
	q.assertNoRawQuery()
	q.orderByTokens = append(q.orderByTokens, orderByTokenScoreDescending)
}

func (q *AbstractDocumentQuery) statistics(stats **QueryStatistics) {
	*stats = q.queryStats
}

func (q *AbstractDocumentQuery) invokeAfterQueryExecuted(result *QueryResult) {
	for _, cb := range q.afterQueryExecutedCallback {
		if cb != nil {
			cb(result)
		}
	}
}

func (q *AbstractDocumentQuery) invokeBeforeQueryExecuted(query *IndexQuery) {
	for _, cb := range q.beforeQueryExecutedCallback {
		if cb != nil {
			cb(query)
		}
	}
}

func (q *AbstractDocumentQuery) invokeAfterStreamExecuted(result map[string]interface{}) {
	for _, cb := range q.afterStreamExecutedCallback {
		if cb != nil {
			cb(result)
		}
	}
}

func (q *AbstractDocumentQuery) GenerateIndexQuery(query string) *IndexQuery {
	indexQuery := NewIndexQuery("")
	indexQuery.query = query
	indexQuery.start = q.start
	indexQuery.waitForNonStaleResults = q.theWaitForNonStaleResults
	indexQuery.waitForNonStaleResultsTimeout = q.timeout
	indexQuery.queryParameters = q.queryParameters
	indexQuery.disableCaching = q.disableCaching

	if q.pageSize != nil {
		indexQuery.pageSize = *q.pageSize
	}
	return indexQuery
}

func (q *AbstractDocumentQuery) search(fieldName string, searchTerms string) {
	q.searchWithOperator(fieldName, searchTerms, SearchOperatorOr)
}

func (q *AbstractDocumentQuery) searchWithOperator(fieldName string, searchTerms string, operator SearchOperator) {
	tokensRef := q.getCurrentWhereTokensRef()
	q.appendOperatorIfNeeded(tokensRef)

	fieldName = q.ensureValidFieldName(fieldName, false)
	q.negateIfNeeded(tokensRef, fieldName)

	whereToken := createWhereTokenWithOptions(WhereOperatorSearch, fieldName, q.addQueryParameter(searchTerms), NewWhereOptionsWithOperator(operator))

	tokens := *tokensRef
	tokens = append(tokens, whereToken)
	*tokensRef = tokens
}

func (q *AbstractDocumentQuery) String() string {
	if q.queryRaw != "" {
		return q.queryRaw
	}

	if q.currentClauseDepth != 0 {
		// throw new IllegalStateError("A clause was not closed correctly within this query, current clause depth = " + _currentClauseDepth);
		panicIf(true, "A clause was not closed correctly within this query, current clause depth = %d", q.currentClauseDepth)
	}

	queryText := &strings.Builder{}
	q.buildDeclare(queryText)
	q.buildFrom(queryText)
	q.buildGroupBy(queryText)
	q.buildWhere(queryText)
	q.buildOrderBy(queryText)

	q.buildLoad(queryText)
	q.buildSelect(queryText)
	q.buildInclude(queryText)

	return queryText.String()
}

func (q *AbstractDocumentQuery) buildInclude(queryText *strings.Builder) {
	if len(q.includes) == 0 {
		return
	}

	q.includes = stringArrayRemoveDuplicates(q.includes)
	queryText.WriteString(" include ")
	for i, include := range q.includes {
		if i > 0 {
			queryText.WriteString(",")
		}

		requiredQuotes := false

		for _, ch := range include {
			if !isLetterOrDigit(ch) && ch != '_' && ch != '.' {
				requiredQuotes = true
				break
			}
		}

		if requiredQuotes {
			s := strings.Replace(include, "'", "\\'", -1)
			queryText.WriteString("'")
			queryText.WriteString(s)
			queryText.WriteString("'")
		} else {
			queryText.WriteString(include)
		}
	}
}

func (q *AbstractDocumentQuery) intersect() {
	tokensRef := q.getCurrentWhereTokensRef()
	tokens := *tokensRef
	n := len(tokens)
	if n > 0 {
		last := tokens[n-1]
		_, isWhere := last.(*whereToken)
		_, isClose := last.(*closeSubclauseToken)
		if isWhere || isClose {
			q.isIntersect = true

			tokens = append(tokens, intersectMarkerTokenInstance)
			*tokensRef = tokens
			return
		}
	}

	//throw new IllegalStateError("Cannot add INTERSECT at this point.");
	panicIf(true, "Cannot add INTERSECT at this point.")
}

func (q *AbstractDocumentQuery) whereExists(fieldName string) {
	fieldName = q.ensureValidFieldName(fieldName, false)

	tokensRef := q.getCurrentWhereTokensRef()
	q.appendOperatorIfNeeded(tokensRef)
	q.negateIfNeeded(tokensRef, fieldName)

	tokens := *tokensRef
	tokens = append(tokens, createWhereToken(WhereOperatorExists, fieldName, ""))
	*tokensRef = tokens
}

func (q *AbstractDocumentQuery) containsAny(fieldName string, values []interface{}) {
	fieldName = q.ensureValidFieldName(fieldName, false)

	tokensRef := q.getCurrentWhereTokensRef()
	q.appendOperatorIfNeeded(tokensRef)
	q.negateIfNeeded(tokensRef, fieldName)

	array := q.transformCollection(fieldName, abstractDocumentQueryUnpackCollection(values))
	whereToken := createWhereTokenWithOptions(WhereOperatorIn, fieldName, q.addQueryParameter(array), NewWhereOptionsWithExact(false))

	tokens := *tokensRef
	tokens = append(tokens, whereToken)
	*tokensRef = tokens
}

func (q *AbstractDocumentQuery) containsAll(fieldName string, values []interface{}) {
	fieldName = q.ensureValidFieldName(fieldName, false)

	tokensRef := q.getCurrentWhereTokensRef()
	q.appendOperatorIfNeeded(tokensRef)
	q.negateIfNeeded(tokensRef, fieldName)

	array := q.transformCollection(fieldName, abstractDocumentQueryUnpackCollection(values))

	tokens := *tokensRef
	if len(array) == 0 {
		tokens = append(tokens, trueTokenInstance)
	} else {
		whereToken := createWhereToken(WhereOperatorAllIn, fieldName, q.addQueryParameter(array))
		tokens = append(tokens, whereToken)
	}
	*tokensRef = tokens
}

func (q *AbstractDocumentQuery) distinct() {
	panicIf(q.isDistinct(), "The is already a distinct query")
	//throw new IllegalStateError("The is already a distinct query");

	if len(q.selectTokens) == 0 {
		q.selectTokens = []queryToken{distinctTokenInstance}
		return
	}
	q.selectTokens = append([]queryToken{distinctTokenInstance}, q.selectTokens...)
}

func (q *AbstractDocumentQuery) updateStatsAndHighlightings(queryResult *QueryResult) {
	q.queryStats.UpdateQueryStats(queryResult)
	//TBD 4.1 Highlightings.Update(queryResult);
}

func (q *AbstractDocumentQuery) buildSelect(writer *strings.Builder) {
	if len(q.selectTokens) == 0 {
		return
	}

	writer.WriteString(" select ")

	if len(q.selectTokens) == 1 {
		tok := q.selectTokens[0]
		if dtok, ok := tok.(*distinctToken); ok {
			dtok.writeTo(writer)
			writer.WriteString(" *")
			return
		}
	}

	for i, token := range q.selectTokens {
		if i > 0 {
			prevToken := q.selectTokens[i-1]
			if _, ok := prevToken.(*distinctToken); !ok {
				writer.WriteString(",")
			}
		}

		var prevToken queryToken
		if i > 0 {
			prevToken = q.selectTokens[i-1]
		}
		documentQueryHelperAddSpaceIfNeeded(prevToken, token, writer)

		token.writeTo(writer)
	}
}

func (q *AbstractDocumentQuery) buildFrom(writer *strings.Builder) {
	q.fromToken.writeTo(writer)
}

func (q *AbstractDocumentQuery) buildDeclare(writer *strings.Builder) {
	if q.declareToken != nil {
		q.declareToken.writeTo(writer)
	}
}

func (q *AbstractDocumentQuery) buildLoad(writer *strings.Builder) {
	if len(q.loadTokens) == 0 {
		return
	}

	writer.WriteString(" load ")

	for i, tok := range q.loadTokens {
		if i != 0 {
			writer.WriteString(", ")
		}

		tok.writeTo(writer)
	}
}

func (q *AbstractDocumentQuery) buildWhere(writer *strings.Builder) {
	if len(q.whereTokens) == 0 {
		return
	}

	writer.WriteString(" where ")

	if q.isIntersect {
		writer.WriteString("intersect(")
	}

	for i, tok := range q.whereTokens {
		var prevToken queryToken
		if i > 0 {
			prevToken = q.whereTokens[i-1]
		}
		documentQueryHelperAddSpaceIfNeeded(prevToken, tok, writer)
		tok.writeTo(writer)
	}

	if q.isIntersect {
		writer.WriteString(") ")
	}
}

func (q *AbstractDocumentQuery) buildGroupBy(writer *strings.Builder) {
	if len(q.groupByTokens) == 0 {
		return
	}

	writer.WriteString(" group by ")

	for i, token := range q.groupByTokens {
		if i > 0 {
			writer.WriteString(", ")
		}
		token.writeTo(writer)
	}
}

func (q *AbstractDocumentQuery) buildOrderBy(writer *strings.Builder) {
	if len(q.orderByTokens) == 0 {
		return
	}

	writer.WriteString(" order by ")

	for i, token := range q.orderByTokens {
		if i > 0 {
			writer.WriteString(", ")
		}

		token.writeTo(writer)
	}
}

func (q *AbstractDocumentQuery) appendOperatorIfNeeded(tokensRef *[]queryToken) {
	tokens := *tokensRef
	q.assertNoRawQuery()

	n := len(tokens)
	if len(tokens) == 0 {
		return
	}

	lastToken := tokens[n-1]
	_, isWhereToken := lastToken.(*whereToken)
	_, isCloseSubclauseToken := lastToken.(*closeSubclauseToken)
	if !isWhereToken && !isCloseSubclauseToken {
		return
	}

	var lastWhere *whereToken

	for i := n - 1; i >= 0; i-- {
		tok := tokens[i]
		if maybeLastWhere, ok := tok.(*whereToken); ok {
			lastWhere = maybeLastWhere
			break
		}
	}

	var token *queryOperatorToken
	if q.defaultOperator == QueryOperatorAnd {
		token = queryOperatorTokenAnd
	} else {
		token = queryOperatorTokenOr
	}

	if lastWhere != nil && lastWhere.options.searchOperator != SearchOperatorUnset {
		token = queryOperatorTokenOr // default to OR operator after search if AND was not specified explicitly
	}

	tokens = append(tokens, token)
	*tokensRef = tokens
}

func (q *AbstractDocumentQuery) transformCollection(fieldName string, values []interface{}) []interface{} {
	var result []interface{}
	for _, value := range values {
		if collectionValue, ok := value.([]interface{}); ok {
			tmp := q.transformCollection(fieldName, collectionValue)
			result = append(result, tmp...)
		} else {
			nestedWhereParams := &whereParams{
				allowWildcards: true,
				fieldName:      fieldName,
				value:          value,
			}
			tmp := q.transformValue(nestedWhereParams)
			result = append(result, tmp)
		}
	}
	return result
}

func (q *AbstractDocumentQuery) negateIfNeeded(tokensRef *[]queryToken, fieldName string) {
	if !q.negate {
		return
	}

	q.negate = false

	tokens := *tokensRef

	n := len(tokens)
	isOpenSubclauseToken := false
	if n > 0 {
		_, isOpenSubclauseToken = tokens[n-1].(*openSubclauseToken)
	}
	if n == 0 || isOpenSubclauseToken {
		if fieldName != "" {
			q.whereExists(fieldName)
		} else {
			q.whereTrue()
		}
		q.andAlso()
	}

	tokens = append(tokens, negateTokenInstance)
	*tokensRef = tokens
}

func abstractDocumentQueryUnpackCollection(items []interface{}) []interface{} {
	var results []interface{}

	for _, item := range items {
		if itemCollection, ok := item.([]interface{}); ok {
			els := abstractDocumentQueryUnpackCollection(itemCollection)
			results = append(results, els...)
		} else {
			results = append(results, item)
		}
	}

	return results
}

func assertValidFieldName(fieldName string) {
	// TODO: for now all names are valid.
	// The code below checks
	if true {
		return
	}
	// in Go only public fields can be serialized so check that first
	// letter is uppercase
	if len(fieldName) == 0 {
		return
	}
	for i, c := range fieldName {
		if i > 0 {
			return
		}
		isUpper := unicode.IsUpper(c)
		panicIf(!isUpper, "field '%s' is not public (doesn't start with uppercase letter)", fieldName)
	}
}

func (q *AbstractDocumentQuery) ensureValidFieldName(fieldName string, isNestedPath bool) string {
	assertValidFieldName(fieldName)
	if q.theSession == nil || q.theSession.GetConventions() == nil || isNestedPath || q.isGroupBy {
		return queryFieldUtilEscapeIfNecessary(fieldName)
	}

	if fieldName == documentConventionsIdentityPropertyName {
		return IndexingFieldNameDocumentID
	}

	return queryFieldUtilEscapeIfNecessary(fieldName)
}

func (q *AbstractDocumentQuery) transformValue(whereParams *whereParams) interface{} {
	return q.transformValueWithRange(whereParams, false)
}

func (q *AbstractDocumentQuery) transformValueWithRange(whereParams *whereParams, forRange bool) interface{} {
	if whereParams.value == nil {
		return nil
	}

	if "" == whereParams.value {
		return ""
	}

	var stringValueReference string
	if q.conventions.TryConvertValueForQuery(whereParams.fieldName, whereParams.value, forRange, &stringValueReference) {
		return stringValueReference
	}

	val := whereParams.value
	switch v := val.(type) {
	case time.Time, string, int, int32, int64, float32, float64, bool:
		return val
	case time.Duration:
		n := int64(v/time.Nanosecond) / 100
		return n
	}
	return whereParams.value
}

func (q *AbstractDocumentQuery) addQueryParameter(value interface{}) string {
	parameterName := "p" + strconv.Itoa(len(q.queryParameters))
	q.queryParameters[parameterName] = value
	return parameterName
}

func (q *AbstractDocumentQuery) getCurrentWhereTokens() []queryToken {
	if !q.isInMoreLikeThis {
		return q.whereTokens
	}

	n := len(q.whereTokens)

	if n == 0 {
		// throw new IllegalStateError("Cannot get moreLikeThisToken because there are no where token specified.");
		panicIf(true, "Cannot get moreLikeThisToken because there are no where token specified.")
	}

	lastToken := q.whereTokens[n-1]

	if moreLikeThisToken, ok := lastToken.(*moreLikeThisToken); ok {
		return moreLikeThisToken.whereTokens
	} else {
		//throw new IllegalStateError("Last token is not moreLikeThisToken");
		panicIf(true, "Last token is not moreLikeThisToken")
	}
	return nil
}

func (q *AbstractDocumentQuery) getCurrentWhereTokensRef() *[]queryToken {
	if !q.isInMoreLikeThis {
		return &q.whereTokens
	}

	n := len(q.whereTokens)

	if n == 0 {
		// throw new IllegalStateError("Cannot get moreLikeThisToken because there are no where token specified.");
		panicIf(true, "Cannot get moreLikeThisToken because there are no where token specified.")
	}

	lastToken := q.whereTokens[n-1]

	if moreLikeThisToken, ok := lastToken.(*moreLikeThisToken); ok {
		return &moreLikeThisToken.whereTokens
	} else {
		//throw new IllegalStateError("Last token is not moreLikeThisToken");
		panicIf(true, "Last token is not moreLikeThisToken")
	}
	return nil
}

func (q *AbstractDocumentQuery) updateFieldsToFetchToken(fieldsToFetch *fieldsToFetchToken) {
	q.fieldsToFetchToken = fieldsToFetch

	if len(q.selectTokens) == 0 {
		q.selectTokens = append(q.selectTokens, fieldsToFetch)
	} else {
		for _, x := range q.selectTokens {
			if _, ok := x.(*fieldsToFetchToken); ok {
				for idx, tok := range q.selectTokens {
					if tok == x {
						q.selectTokens[idx] = fieldsToFetch
					}
				}
				return
			}
		}
		q.selectTokens = append(q.selectTokens, fieldsToFetch)
	}
}

func getSourceAliasIfExists(clazz reflect.Type, queryData *QueryData, fields []string) string {
	if len(fields) != 1 || fields[0] == "" {
		return ""
	}

	if clazz != reflect.TypeOf("") && !isPrimitiveOrWrapper(clazz) {
		return ""
	}
	indexOf := strings.Index(fields[0], ".")
	if indexOf == -1 {
		return ""
	}

	possibleAlias := fields[0][:indexOf]
	if queryData.FromAlias == possibleAlias {
		return possibleAlias
	}

	if len(queryData.LoadTokens) == 0 {
		return ""
	}

	// TODO: is this the logic?
	for _, x := range queryData.LoadTokens {
		if x.alias == possibleAlias {
			return possibleAlias
		}
	}
	return ""
}

func (q *AbstractDocumentQuery) addBeforeQueryExecutedListener(action func(*IndexQuery)) int {
	q.beforeQueryExecutedCallback = append(q.beforeQueryExecutedCallback, action)
	return len(q.beforeQueryExecutedCallback) - 1
}

func (q *AbstractDocumentQuery) removeBeforeQueryExecutedListener(idx int) {
	q.beforeQueryExecutedCallback[idx] = nil
}

func (q *AbstractDocumentQuery) addAfterQueryExecutedListener(action func(*QueryResult)) int {
	q.afterQueryExecutedCallback = append(q.afterQueryExecutedCallback, action)
	return len(q.afterQueryExecutedCallback) - 1
}

func (q *AbstractDocumentQuery) removeAfterQueryExecutedListener(idx int) {
	q.afterQueryExecutedCallback[idx] = nil
}

func (q *AbstractDocumentQuery) addAfterStreamExecutedListener(action func(map[string]interface{})) int {
	q.afterStreamExecutedCallback = append(q.afterStreamExecutedCallback, action)
	return len(q.afterStreamExecutedCallback) - 1
}

func (q *AbstractDocumentQuery) removeAfterStreamExecutedListener(idx int) {
	q.afterStreamExecutedCallback[idx] = nil
}

func (q *AbstractDocumentQuery) noTracking() {
	q.disableEntitiesTracking = true
}

func (q *AbstractDocumentQuery) noCaching() {
	q.disableCaching = true
}

func (q *AbstractDocumentQuery) withinRadiusOf(fieldName string, radius float64, latitude float64, longitude float64, radiusUnits SpatialUnits, distErrorPercent float64) {
	fieldName = q.ensureValidFieldName(fieldName, false)

	tokensRef := q.getCurrentWhereTokensRef()
	q.appendOperatorIfNeeded(tokensRef)
	q.negateIfNeeded(tokensRef, fieldName)

	shape := ShapeTokenCircle(q.addQueryParameter(radius), q.addQueryParameter(latitude), q.addQueryParameter(longitude), radiusUnits)
	opts := NewWhereOptionsWithTokenAndDistance(shape, distErrorPercent)
	whereToken := createWhereTokenWithOptions(WhereOperatorSpatialWithin, fieldName, "", opts)

	tokens := *tokensRef
	tokens = append(tokens, whereToken)
	*tokensRef = tokens
}

func (q *AbstractDocumentQuery) spatial(fieldName string, shapeWkt string, relation SpatialRelation, distErrorPercent float64) {
	fieldName = q.ensureValidFieldName(fieldName, false)

	tokensRef := q.getCurrentWhereTokensRef()
	q.appendOperatorIfNeeded(tokensRef)
	q.negateIfNeeded(tokensRef, fieldName)

	wktToken := ShapeTokenWkt(q.addQueryParameter(shapeWkt))

	var whereOperator WhereOperator
	switch relation {
	case SpatialRelationWithin:
		whereOperator = WhereOperatorSpatialWithin
	case SpatialRelationContains:
		whereOperator = WhereOperatorSpatialContains
	case SpatialRelationDisjoin:
		whereOperator = WhereOperatorSpatialDisjoint
	case SpatialRelationIntersects:
		whereOperator = WhereOperatorSpatialIntersects
	default:
		//throw new IllegalArgumentError();
		panicIf(true, "unknown relation %s", relation)
	}

	tokens := *tokensRef
	opts := NewWhereOptionsWithTokenAndDistance(wktToken, distErrorPercent)
	tok := createWhereTokenWithOptions(whereOperator, fieldName, "", opts)
	tokens = append(tokens, tok)
	*tokensRef = tokens
}

func (q *AbstractDocumentQuery) spatial2(dynamicField DynamicSpatialField, criteria SpatialCriteria) {
	must(q.assertIsDynamicQuery(dynamicField, "spatial"))

	tokensRef := q.getCurrentWhereTokensRef()
	q.appendOperatorIfNeeded(tokensRef)
	q.negateIfNeeded(tokensRef, "")

	ensure := func(fieldName string, isNestedPath bool) string {
		return q.ensureValidFieldName(fieldName, isNestedPath)
	}
	add := func(value interface{}) string {
		return q.addQueryParameter(value)
	}
	tok := criteria.ToQueryToken(dynamicField.ToField(ensure), add)
	tokens := *tokensRef
	tokens = append(tokens, tok)
	*tokensRef = tokens
}

func (q *AbstractDocumentQuery) spatial3(fieldName string, criteria SpatialCriteria) {
	fieldName = q.ensureValidFieldName(fieldName, false)

	tokensRef := q.getCurrentWhereTokensRef()
	q.appendOperatorIfNeeded(tokensRef)
	q.negateIfNeeded(tokensRef, fieldName)

	tokens := *tokensRef
	add := func(value interface{}) string {
		return q.addQueryParameter(value)
	}
	tok := criteria.ToQueryToken(fieldName, add)
	tokens = append(tokens, tok)
	*tokensRef = tokens
}

func (q *AbstractDocumentQuery) orderByDistance(field DynamicSpatialField, latitude float64, longitude float64) {
	if field == nil {
		//throw new IllegalArgumentError("Field cannot be null");
		panicIf(true, "Field cannot be null")
	}
	must(q.assertIsDynamicQuery(field, "orderByDistance"))

	ensure := func(fieldName string, isNestedPath bool) string {
		return q.ensureValidFieldName(fieldName, isNestedPath)
	}

	q.orderByDistanceLatLong("'"+field.ToField(ensure)+"'", latitude, longitude)
}

func (q *AbstractDocumentQuery) orderByDistanceLatLong(fieldName string, latitude float64, longitude float64) {
	tok := orderByTokenCreateDistanceAscending(fieldName, q.addQueryParameter(latitude), q.addQueryParameter(longitude))
	q.orderByTokens = append(q.orderByTokens, tok)
}

func (q *AbstractDocumentQuery) orderByDistance2(field DynamicSpatialField, shapeWkt string) {
	if field == nil {
		//throw new IllegalArgumentError("Field cannot be null");
		panicIf(true, "Field cannot be null")
	}
	must(q.assertIsDynamicQuery(field, "orderByDistance2"))

	ensure := func(fieldName string, isNestedPath bool) string {
		return q.ensureValidFieldName(fieldName, isNestedPath)
	}
	q.orderByDistance3("'"+field.ToField(ensure)+"'", shapeWkt)
}

func (q *AbstractDocumentQuery) orderByDistance3(fieldName string, shapeWkt string) {
	tok := orderByTokenCreateDistanceAscending2(fieldName, q.addQueryParameter(shapeWkt))
	q.orderByTokens = append(q.orderByTokens, tok)
}

func (q *AbstractDocumentQuery) orderByDistanceDescending(field DynamicSpatialField, latitude float64, longitude float64) {
	if field == nil {
		//throw new IllegalArgumentError("Field cannot be null");
		panicIf(true, "Field cannot be null")
	}
	must(q.assertIsDynamicQuery(field, "orderByDistanceDescending"))
	ensure := func(fieldName string, isNestedPath bool) string {
		return q.ensureValidFieldName(fieldName, isNestedPath)
	}
	q.orderByDistanceDescendingLatLong("'"+field.ToField(ensure)+"'", latitude, longitude)
}

func (q *AbstractDocumentQuery) orderByDistanceDescendingLatLong(fieldName string, latitude float64, longitude float64) {
	tok := orderByTokenCreateDistanceDescending(fieldName, q.addQueryParameter(latitude), q.addQueryParameter(longitude))
	q.orderByTokens = append(q.orderByTokens, tok)
}

func (q *AbstractDocumentQuery) orderByDistanceDescending2(field DynamicSpatialField, shapeWkt string) {
	if field == nil {
		//throw new IllegalArgumentError("Field cannot be null");
		panicIf(true, "Field cannot be null")
	}
	must(q.assertIsDynamicQuery(field, "orderByDistanceDescending2"))
	ensure := func(fieldName string, isNestedPath bool) string {
		return q.ensureValidFieldName(fieldName, isNestedPath)
	}
	q.orderByDistanceDescending3("'"+field.ToField(ensure)+"'", shapeWkt)
}

func (q *AbstractDocumentQuery) orderByDistanceDescending3(fieldName string, shapeWkt string) {
	tok := orderByTokenCreateDistanceDescending2(fieldName, q.addQueryParameter(shapeWkt))
	q.orderByTokens = append(q.orderByTokens, tok)
}

func (q *AbstractDocumentQuery) assertIsDynamicQuery(dynamicField DynamicSpatialField, methodName string) error {
	if q.fromToken != nil && !q.fromToken.isDynamic {
		f := func(s string, f bool) string {
			return q.ensureValidFieldName(s, f)
		}
		fld := dynamicField.ToField(f)
		return newIllegalStateError("Cannot execute query method '" + methodName + "'. Field '" + fld + "' cannot be used when static index '" + q.fromToken.indexName + "' is queried. Dynamic spatial fields can only be used with dynamic queries, " + "for static index queries please use valid spatial fields defined in index definition.")
	}
	return nil
}

func (q *AbstractDocumentQuery) initSync() error {
	if q.queryOperation != nil {
		return nil
	}

	delegate := &DocumentQueryCustomization{
		query: q,
	}
	beforeQueryEventArgs := &BeforeQueryEventArgs{
		Session:            q.theSession,
		QueryCustomization: delegate,
	}
	q.theSession.OnBeforeQueryInvoke(beforeQueryEventArgs)

	var err error
	q.queryOperation, err = q.initializeQueryOperation()
	if err != nil {
		return err
	}
	return q.executeActualQuery()
}

func (q *AbstractDocumentQuery) executeActualQuery() error {
	{
		context := q.queryOperation.enterQueryContext()
		command, err := q.queryOperation.createRequest()
		if err != nil {
			return err
		}
		err = q.theSession.GetRequestExecutor().ExecuteCommand(command, q.theSession.sessionInfo)
		q.queryOperation.setResult(command.Result)
		context.Close()
		if err != nil {
			return err
		}
	}
	q.invokeAfterQueryExecuted(q.queryOperation.currentQueryResults)
	return nil
}

// GetQueryResult returns results of a query
func (q *AbstractDocumentQuery) GetQueryResult() (*QueryResult, error) {
	err := q.initSync()
	if err != nil {
		return nil, err
	}

	return q.queryOperation.currentQueryResults.createSnapshot(), nil
}

// given *[]<type> returns <type>
func getTypeFromQueryResults(results interface{}) (reflect.Type, error) {
	rt := reflect.TypeOf(results)
	if (rt.Kind() == reflect.Ptr) && (rt.Elem() != nil) && (rt.Elem().Kind() == reflect.Slice) {
		return rt.Elem().Elem(), nil
	}
	return nil, fmt.Errorf("expected value of type *[]<type>, got %T", results)
}

func (q *AbstractDocumentQuery) setClazzFromResult(result interface{}) {
	if q.clazz != nil {
		return
	}

	var err error
	// it's possible to search in index without needing to know the type
	// see moreLikeThisCanGetResultsUsingTermVectorsLazy test
	if q.fromToken != nil {
		q.clazz, err = getTypeFromQueryResults(result)
		if err != nil {
			panic(err.Error())
		}
		return
	}

	// query was created without providing the type to query
	panicIf(q.indexName != "", "q.clazz is not set but q.indexName is")
	panicIf(q.collectionName != "", "q.clazz is not set but q.collectionName is")
	panicIf(q.fromToken != nil, "q.clazz is not set but q.fromToken is")
	tp := reflect.TypeOf(result)
	if tp2, ok := isPtrPtrStruct(tp); ok {
		q.clazz = tp2
	} else if tp2, ok := isPtrSlicePtrStruct(tp); ok {
		q.clazz = tp2
	} else {
		panicIf(true, "expected result to be **struct or *[]*struct, got: %T", result)
	}
	s := q.theSession
	indexName, collectionName := s.processQueryParameters(q.clazz, "", "", s.GetConventions())
	q.fromToken = createFromToken(indexName, collectionName, "")
}

// GetResults executes the query and sets results to returned values.
// results should be of type *[]<type>
// TODO: name it Execute() instead?
// Note: ToList in java
func (q *AbstractDocumentQuery) GetResults(results interface{}) error {
	if results == nil {
		return fmt.Errorf("results can't be nil")
	}

	// delayed SelectFields logic
	if q.selectFieldsArgs != nil {
		hadClazz := (q.clazz != nil)
		// query was created without providing the type to query
		projectionClass, err := getTypeFromQueryResults(results)
		if err != nil {
			return err
		}
		dq := q.createDocumentQueryInternal(projectionClass, q.selectFieldsArgs)
		q = dq.AbstractDocumentQuery
		panicIf(q.clazz != projectionClass, "q.clazz != projectionClass")
		if !hadClazz {
			s := q.theSession
			q.indexName, q.collectionName = s.processQueryParameters(q.clazz, q.indexName, q.collectionName, s.GetConventions())
			q.fromToken = createFromToken(q.indexName, q.collectionName, "")
		}
	}

	if q.clazz == nil {
		// query was created without providing the type to query
		var err error
		q.clazz, err = getTypeFromQueryResults(results)
		if err != nil {
			return err
		}
		s := q.theSession
		q.indexName, q.collectionName = s.processQueryParameters(q.clazz, q.indexName, q.collectionName, s.GetConventions())
		q.fromToken = createFromToken(q.indexName, q.collectionName, "")
	}

	return q.executeQueryOperation(results, 0)
}

// First runs a query and returns a first result.
func (q *AbstractDocumentQuery) First(result interface{}) error {
	if result == nil {
		return newIllegalArgumentError("result can't be nil")
	}
	q.setClazzFromResult(result)

	tp := reflect.TypeOf(result)
	// **struct => *struct
	if tp.Kind() == reflect.Ptr && tp.Elem().Kind() == reflect.Ptr {
		tp = tp.Elem()
	}
	// create a pointer to a slice. executeQueryOperation creates the actual slice
	sliceType := reflect.SliceOf(tp)
	slicePtr := reflect.New(sliceType)
	err := q.executeQueryOperation(slicePtr.Interface(), 1)
	if err != nil {
		return err
	}
	slice := slicePtr.Elem()
	if slice.Len() == 0 {
		return newIllegalStateError("Expectecd at least one result")
	}
	el := slice.Index(0)
	setInterfaceToValue(result, el.Interface())
	return nil
}

// Single runs a query that expects only a single result.
// If there is more than one result, it retuns IllegalStateError.
func (q *AbstractDocumentQuery) Single(result interface{}) error {
	if result == nil {
		return fmt.Errorf("result can't be nil")
	}
	q.setClazzFromResult(result)

	tp := reflect.TypeOf(result)
	// **struct => *struct
	if tp.Kind() == reflect.Ptr && tp.Elem().Kind() == reflect.Ptr {
		tp = tp.Elem()
	}
	// create a pointer to a slice. executeQueryOperation creates the actual slice
	sliceType := reflect.SliceOf(tp)
	slicePtr := reflect.New(sliceType)
	err := q.executeQueryOperation(slicePtr.Interface(), 2)
	if err != nil {
		return err
	}
	slice := slicePtr.Elem()
	if slice.Len() != 1 {
		return newIllegalStateError("Expected single result, got: %d", slice.Len())
	}
	el := slice.Index(0)
	setInterfaceToValue(result, el.Interface())
	return nil
}

func (q *AbstractDocumentQuery) Count() (int, error) {
	{
		var tmp = 0
		q.take(&tmp)
	}
	queryResult, err := q.GetQueryResult()
	if err != nil {
		return 0, err
	}
	return queryResult.TotalResults, nil
}

// Any returns true if query returns at least one result
// TODO: write tests
func (q *AbstractDocumentQuery) Any() (bool, error) {
	if q.isDistinct() {
		// for distinct it is cheaper to do count 1

		tp := q.clazz
		// **struct => *struct
		if tp.Kind() == reflect.Ptr && tp.Elem().Kind() == reflect.Ptr {
			tp = tp.Elem()
		}
		// create a pointer to a slice. executeQueryOperation creates the actual slice
		sliceType := reflect.SliceOf(tp)
		slicePtr := reflect.New(sliceType)
		err := q.executeQueryOperation(slicePtr.Interface(), 1)
		if err != nil {
			return false, err
		}
		slice := slicePtr.Elem()
		return slice.Len() > 0, nil
	}

	{
		var tmp = 0
		q.take(&tmp)
	}
	queryResult, err := q.GetQueryResult()
	if err != nil {
		return false, err
	}
	return queryResult.TotalResults > 0, nil
}

func (q *AbstractDocumentQuery) executeQueryOperation(results interface{}, take int) error {
	if take != 0 && (q.pageSize == nil || *q.pageSize > take) {
		q.take(&take)
	}

	err := q.initSync()
	if err != nil {
		return err
	}

	return q.queryOperation.complete(results)
}

func (q *AbstractDocumentQuery) aggregateBy(facet FacetBase) error {
	for _, token := range q.selectTokens {
		if _, ok := token.(*facetToken); ok {
			continue
		}

		return newIllegalStateError("Aggregation query can select only facets while it got %T token", token)
	}

	add := func(o interface{}) string {
		return q.addQueryParameter(o)
	}
	t, err := createFacetTokenWithFacetBase(facet, add)
	if err != nil {
		return err
	}
	q.selectTokens = append(q.selectTokens, t)
	return nil
}

func (q *AbstractDocumentQuery) aggregateUsing(facetSetupDocumentID string) {
	q.selectTokens = append(q.selectTokens, createFacetToken(facetSetupDocumentID))
}

func (q *AbstractDocumentQuery) Lazily(results interface{}, onEval func(interface{})) (*Lazy, error) {
	q.setClazzFromResult(results)
	if q.queryOperation == nil {
		var err error
		q.queryOperation, err = q.initializeQueryOperation()
		if err != nil {
			return nil, err
		}
	}

	lazyQueryOperation := NewLazyQueryOperation(results, q.theSession.GetConventions(), q.queryOperation, q.afterQueryExecutedCallback)
	return q.theSession.session.addLazyOperation(results, lazyQueryOperation, onEval), nil
}

// CountLazily returns a lazy operation that returns number of results in a query. It'll set *count to
// number of results after Lazy.GetResult() is called.
// results should be of type []<type> and is only provided so that we know this is a query for <type>
// TODO: figure out better API.
func (q *AbstractDocumentQuery) CountLazily(results interface{}, count *int) (*Lazy, error) {
	if count == nil {
		return nil, newIllegalArgumentError("count can't be nil")
	}
	if q.queryOperation == nil {
		v := 0
		q.take(&v)
		var err error
		q.queryOperation, err = q.initializeQueryOperation()
		if err != nil {
			return nil, err
		}
	}

	lazyQueryOperation := NewLazyQueryOperation(results, q.theSession.GetConventions(), q.queryOperation, q.afterQueryExecutedCallback)
	return q.theSession.session.addLazyCountOperation(count, lazyQueryOperation), nil
}

// suggestUsing adds a query part for suggestions
func (q *AbstractDocumentQuery) suggestUsing(suggestion SuggestionBase) error {
	if suggestion == nil {
		return newIllegalArgumentError("suggestion cannot be null")
	}

	if err := q.assertCanSuggest(); err != nil {
		return err
	}

	var token *suggestToken

	if term, ok := suggestion.(*SuggestionWithTerm); ok {
		token = &suggestToken{
			fieldName:            term.Field,
			termParameterName:    q.addQueryParameter(term.Term),
			optionsParameterName: q.getOptionsParameterName(term.Options),
		}
	} else if terms, ok := suggestion.(*SuggestionWithTerms); ok {
		token = &suggestToken{
			fieldName:            terms.Field,
			termParameterName:    q.addQueryParameter(terms.Terms),
			optionsParameterName: q.getOptionsParameterName(terms.Options),
		}
	} else {
		return newUnsupportedOperationError("Unknown type of suggestion: %T", suggestion)
	}
	q.selectTokens = append(q.selectTokens, token)
	return nil
}

func (q *AbstractDocumentQuery) getOptionsParameterName(options *SuggestionOptions) string {
	optionsParameterName := ""
	if options != nil && options != SuggestionOptionsDefaultOptions {
		optionsParameterName = q.addQueryParameter(options)
	}

	return optionsParameterName
}

func (q *AbstractDocumentQuery) assertCanSuggest() error {
	if len(q.whereTokens) > 0 {
		return newIllegalStateError("Cannot add suggest when WHERE statements are present.")
	}

	if len(q.selectTokens) > 0 {
		return newIllegalStateError("Cannot add suggest when SELECT statements are present.")
	}

	if len(q.orderByTokens) > 0 {
		return newIllegalStateError("Cannot add suggest when ORDER BY statements are present.")
	}
	return nil
}
