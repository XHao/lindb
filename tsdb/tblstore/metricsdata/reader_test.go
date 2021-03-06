package metricsdata

import (
	"fmt"
	"math"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/lindb/roaring"
	"github.com/stretchr/testify/assert"

	"github.com/lindb/lindb/aggregation"
	"github.com/lindb/lindb/flow"
	"github.com/lindb/lindb/kv"
	"github.com/lindb/lindb/pkg/bit"
	"github.com/lindb/lindb/pkg/encoding"
	"github.com/lindb/lindb/series"
	"github.com/lindb/lindb/series/field"
)

var bitmapUnmarshal = encoding.BitmapUnmarshal

func TestNewReader(t *testing.T) {
	defer func() {
		encoding.BitmapUnmarshal = bitmapUnmarshal
	}()
	// case 1: footer err
	r, err := NewReader("1.sst", []byte{1, 2, 3})
	assert.Error(t, err)
	assert.Nil(t, r)
	// case 2: offset err
	r, err = NewReader("1.sst", []byte{0, 0, 0, 1, 2, 3, 3, 1, 1, 1, 1, 2, 2, 2, 2, 3, 3, 3, 3, 1, 2, 3, 4})
	assert.Error(t, err)
	assert.Nil(t, r)
	// case 3: new reader success
	r, err = NewReader("1.sst", mockMetricBlock())
	assert.NoError(t, err)
	assert.NotNil(t, r)
	assert.Equal(t, "1.sst", r.Path())
	start, end := r.GetTimeRange()
	assert.Equal(t, uint16(5), start)
	assert.Equal(t, uint16(5), end)
	assert.Equal(t, field.Metas{
		{ID: 2, Type: field.SumField},
		{ID: 10, Type: field.MinField},
		{ID: 30, Type: field.SumField},
		{ID: 100, Type: field.MaxField},
	}, r.GetFields())
	seriesIDs := roaring.New()
	for j := 0; j < 10; j++ {
		seriesIDs.Add(uint32(j * 4096))
	}
	seriesIDs.Add(65536 + 10)
	assert.EqualValues(t, seriesIDs.ToArray(), r.GetSeriesIDs().ToArray())
	// case 4: unmarshal series ids err
	encoding.BitmapUnmarshal = func(bitmap *roaring.Bitmap, data []byte) error {
		return fmt.Errorf("err")
	}
	r, err = NewReader("1.sst", mockMetricBlock())
	assert.Error(t, err)
	assert.Nil(t, r)
}

func TestReader_Load(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	qFlow := flow.NewMockStorageQueryFlow(ctrl)

	r, err := NewReader("1.sst", mockMetricBlock())
	assert.NoError(t, err)
	assert.NotNil(t, r)
	// case 1: series high key not found
	r.Load(qFlow, 10, []field.ID{2, 30, 50}, 1000, nil)
	// case 2: load success
	sAgg1 := aggregation.NewMockSeriesAggregator(ctrl)
	sAgg2 := aggregation.NewMockSeriesAggregator(ctrl)
	fAgg1 := aggregation.NewMockFieldAggregator(ctrl)
	fAgg2 := aggregation.NewMockFieldAggregator(ctrl)
	block1 := series.NewMockBlock(ctrl)
	// case 2: load data success
	gomock.InOrder(
		qFlow.EXPECT().GetAggregator().Return(aggregation.FieldAggregates{sAgg1, sAgg2, nil}),
		sAgg1.EXPECT().GetAggregator(int64(10)).Return(fAgg1, true),
		fAgg1.EXPECT().GetBlock().Return(block1),
		sAgg2.EXPECT().GetAggregator(int64(10)).Return(fAgg2, false),
		block1.EXPECT().Append(5, 0.0).Times(2),
		qFlow.EXPECT().Reduce("host", gomock.Any()),
	)
	r, err = NewReader("1.sst", mockMetricBlock())
	assert.NoError(t, err)
	r.Load(qFlow, 10, []field.ID{2, 30, 50}, 0, map[string][]uint16{"host": {4096, 8192}})
	// case 3: can't get aggregator by family
	gomock.InOrder(
		qFlow.EXPECT().GetAggregator().Return(aggregation.FieldAggregates{sAgg1, sAgg2, nil}),
		sAgg1.EXPECT().GetAggregator(int64(10)).Return(fAgg1, false),
		sAgg2.EXPECT().GetAggregator(int64(10)).Return(fAgg2, false),
		qFlow.EXPECT().Reduce("host", gomock.Any()),
	)
	r.Load(qFlow, 10, []field.ID{2, 30, 50}, 0, map[string][]uint16{"host": {4096, 8192}})
	// case 3: series ids not found
	gomock.InOrder(
		qFlow.EXPECT().GetAggregator().Return(aggregation.FieldAggregates{sAgg1, sAgg2, nil}),
		sAgg1.EXPECT().GetAggregator(int64(10)).Return(fAgg1, true),
		fAgg1.EXPECT().GetBlock().Return(block1),
		sAgg2.EXPECT().GetAggregator(int64(10)).Return(fAgg2, false),
		qFlow.EXPECT().Reduce("host", gomock.Any()),
	)
	r, err = NewReader("1.sst", mockMetricBlock())
	assert.NoError(t, err)
	r.Load(qFlow, 10, []field.ID{2, 30, 50}, 0, map[string][]uint16{"host": {10, 12}})
	// case 4: field not found
	gomock.InOrder(
		qFlow.EXPECT().GetAggregator().Return(aggregation.FieldAggregates{sAgg1, sAgg2, nil}),
		sAgg1.EXPECT().GetAggregator(int64(10)).Return(fAgg1, true),
		fAgg1.EXPECT().GetBlock().Return(block1),
		qFlow.EXPECT().Reduce("host", gomock.Any()),
	)
	r, err = NewReader("1.sst", mockMetricBlock())
	assert.NoError(t, err)
	r.Load(qFlow, 10, []field.ID{100}, 1, map[string][]uint16{"host": {10}})
	// case 5: load data success, but time slot not in query range
	gomock.InOrder(
		qFlow.EXPECT().GetAggregator().Return(aggregation.FieldAggregates{sAgg1, sAgg2, nil}),
		sAgg1.EXPECT().GetAggregator(int64(10)).Return(fAgg1, true),
		fAgg1.EXPECT().GetBlock().Return(block1),
		sAgg2.EXPECT().GetAggregator(int64(10)).Return(fAgg2, false),
		block1.EXPECT().Append(5, 0.0).Times(2),
		qFlow.EXPECT().Reduce("host", gomock.Any()),
	)
	r, err = NewReader("1.sst", mockMetricBlock())
	assert.NoError(t, err)
	r.Load(qFlow, 10, []field.ID{2, 30, 50}, 0, map[string][]uint16{"host": {4096, 8192}})
	// case 6: load data success, metric has one field
	gomock.InOrder(
		qFlow.EXPECT().GetAggregator().Return(aggregation.FieldAggregates{sAgg1, sAgg2, nil}),
		sAgg1.EXPECT().GetAggregator(int64(10)).Return(fAgg1, true),
		fAgg1.EXPECT().GetBlock().Return(block1),
		block1.EXPECT().Append(5, 0.0).Times(2),
		qFlow.EXPECT().Reduce("host", gomock.Any()),
	)
	r, err = NewReader("1.sst", mockMetricBlockForOneField())
	assert.NoError(t, err)
	r.Load(qFlow, 10, []field.ID{2}, 0, map[string][]uint16{"host": {4096, 8192}})
}

func TestReader_scan(t *testing.T) {
	defer func() {
		getOffsetFunc = getOffset
	}()

	r, err := NewReader("1.sst", mockMetricBlock())
	assert.NoError(t, err)
	assert.NotNil(t, r)
	scanner := newDataScanner(r)
	start, end := scanner.slotRange()
	assert.Equal(t, uint16(5), start)
	assert.Equal(t, uint16(5), end)
	// case 1: not match
	seriesPos := scanner.scan(10, 10)
	assert.True(t, seriesPos < 0)
	// case 2: merge data
	scanner = newDataScanner(r)
	seriesPos = scanner.scan(0, 0)
	assert.True(t, seriesPos >= 0)
	seriesPos = scanner.scan(1, 10)
	assert.True(t, seriesPos >= 0)
	// case 3: scan completed
	seriesPos = scanner.scan(3, 10)
	assert.True(t, seriesPos < 0)
	// case 4: not match
	scanner = newDataScanner(r)
	seriesPos = scanner.scan(0, 10)
	assert.True(t, seriesPos < 0)
	// case 6: get wrong offset
	scanner = newDataScanner(r)
	getOffsetFunc = func(seriesOffsets *encoding.FixedOffsetDecoder, idx int) (int, bool) {
		return 0, false
	}
	seriesPos = scanner.scan(0, 0)
	assert.True(t, seriesPos < 0)
}

func mockMetricBlock() []byte {
	nopKVFlusher := kv.NewNopFlusher()
	flusher := NewFlusher(nopKVFlusher)
	flusher.FlushFieldMetas(field.Metas{
		{ID: 2, Type: field.SumField},
		{ID: 10, Type: field.MinField},
		{ID: 30, Type: field.SumField},
		{ID: 100, Type: field.MaxField},
	})
	for j := 0; j < 10; j++ {
		encoder := encoding.NewTSDEncoder(5)
		for i := 0; i < 10; i++ {
			encoder.AppendTime(bit.One)
			encoder.AppendValue(math.Float64bits(float64(10.0 * i)))
		}
		data, _ := encoder.BytesWithoutTime()
		flusher.FlushField(data)
		flusher.FlushField(data)
		flusher.FlushField(data)
		flusher.FlushField(data)
		flusher.FlushSeries(uint32(j * 4096))
	}
	// mock just has one field
	encoder := encoding.NewTSDEncoder(5)
	encoder.AppendTime(bit.One)
	encoder.AppendValue(math.Float64bits(10.0))
	data, _ := encoder.BytesWithoutTime()
	flusher.FlushField(data)
	flusher.FlushSeries(uint32(65536 + 10))
	_ = flusher.FlushMetric(uint32(10), 5, 5)

	return nopKVFlusher.Bytes()
}

func mockMetricBlockForOneField() []byte {
	nopKVFlusher := kv.NewNopFlusher()
	flusher := NewFlusher(nopKVFlusher)
	flusher.FlushFieldMetas(field.Metas{
		{ID: 2, Type: field.SumField},
	})
	for j := 0; j < 10; j++ {
		encoder := encoding.NewTSDEncoder(5)
		for i := 0; i < 10; i++ {
			encoder.AppendTime(bit.One)
			encoder.AppendValue(math.Float64bits(float64(10.0 * i)))
		}
		data, _ := encoder.BytesWithoutTime()
		flusher.FlushField(data)
		flusher.FlushSeries(uint32(j * 4096))
	}
	// mock just has one field
	encoder := encoding.NewTSDEncoder(5)
	encoder.AppendTime(bit.One)
	encoder.AppendValue(math.Float64bits(10.0))
	data, _ := encoder.BytesWithoutTime()
	flusher.FlushField(data)
	flusher.FlushSeries(uint32(65536 + 10))
	_ = flusher.FlushMetric(uint32(10), 5, 5)
	return nopKVFlusher.Bytes()
}
