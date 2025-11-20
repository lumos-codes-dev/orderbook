package engine

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

// TestCancelUnfilledOrder tests cancelling an order that hasn't been matched
func TestCancelUnfilledOrder(t *testing.T) {
	book := NewOrderBook("BTC-USD")

	order := Order{
		ID:    "ORDER-001",
		Side:  Buy,
		Price: decimal.NewFromFloat(50000),
		Qty:   decimal.NewFromFloat(1.0),
		Time:  time.Now().Unix(),
	}

	// Add the order to the book
	fillCh := make(chan OrderFill, 10)
	tradeCh := make(chan Trade, 10)
	book.Match(order, tradeCh, fillCh, order.Qty)
	time.Sleep(50 * time.Millisecond)

	// Clear the channels
	for len(fillCh) > 0 {
		<-fillCh
	}
	for len(tradeCh) > 0 {
		<-tradeCh
	}

	// Cancel the unfilled order
	cancelFillCh := make(chan OrderFill, 1)
	success := book.Cancel("ORDER-001", cancelFillCh)

	if !success {
		t.Fatal("Cancel should have succeeded for unfilled order")
	}

	fill := <-cancelFillCh

	// Verify the fill event
	if fill.Status != Cancelled {
		t.Errorf("Expected status Cancelled, got %s", fill.Status)
	}

	if !fill.OriginalQty.Equal(decimal.NewFromFloat(1.0)) {
		t.Errorf("Expected OriginalQty 1.0, got %s", fill.OriginalQty)
	}

	if !fill.ExecutedQty.IsZero() {
		t.Errorf("Expected ExecutedQty 0, got %s", fill.ExecutedQty)
	}

	if !fill.RemainingQty.IsZero() {
		t.Errorf("Expected RemainingQty 0, got %s", fill.RemainingQty)
	}
}

// TestCancelPartiallyFilledOrder tests cancelling an order that has been partially matched
func TestCancelPartiallyFilledOrder(t *testing.T) {
	book := NewOrderBook("BTC-USD")

	// Add a buy order for 2.0 BTC
	buyOrder := Order{
		ID:    "BUY-001",
		Side:  Buy,
		Price: decimal.NewFromFloat(50000),
		Qty:   decimal.NewFromFloat(2.0),
		Time:  time.Now().Unix(),
	}
	fillCh := make(chan OrderFill, 10)
	tradeCh := make(chan Trade, 10)
	book.Match(buyOrder, tradeCh, fillCh, buyOrder.Qty)
	time.Sleep(50 * time.Millisecond)

	// Clear the channels
	for len(fillCh) > 0 {
		<-fillCh
	}
	for len(tradeCh) > 0 {
		<-tradeCh
	}

	// Add a sell order for 0.75 BTC that partially fills the buy order
	sellOrder := Order{
		ID:    "SELL-001",
		Side:  Sell,
		Price: decimal.NewFromFloat(49500),
		Qty:   decimal.NewFromFloat(0.75),
		Time:  time.Now().Unix(),
	}
	fillCh = make(chan OrderFill, 10)
	tradeCh = make(chan Trade, 10)
	book.Match(sellOrder, tradeCh, fillCh, sellOrder.Qty)
	time.Sleep(50 * time.Millisecond)

	// Clear the channels
	for len(fillCh) > 0 {
		<-fillCh
	}
	for len(tradeCh) > 0 {
		<-tradeCh
	}

	// Cancel the partially filled order
	cancelFillCh := make(chan OrderFill, 1)
	success := book.Cancel("BUY-001", cancelFillCh)

	if !success {
		t.Fatal("Cancel should have succeeded for partially filled order")
	}

	fill := <-cancelFillCh

	// Verify the fill event values
	expectedOriginalQty := decimal.NewFromFloat(2.0)
	expectedExecutedQty := decimal.NewFromFloat(0.75)

	if !fill.OriginalQty.Equal(expectedOriginalQty) {
		t.Errorf("Expected OriginalQty %s, got %s", expectedOriginalQty, fill.OriginalQty)
	}

	if !fill.ExecutedQty.Equal(expectedExecutedQty) {
		t.Errorf("Expected ExecutedQty %s, got %s", expectedExecutedQty, fill.ExecutedQty)
	}

	if !fill.RemainingQty.IsZero() {
		t.Errorf("Expected RemainingQty 0, got %s", fill.RemainingQty)
	}

	if fill.Status != Cancelled {
		t.Errorf("Expected status Cancelled, got %s", fill.Status)
	}
}

// TestCancelFullyFilledOrder tests that cancelling a fully filled order fails
func TestCancelFullyFilledOrder(t *testing.T) {
	book := NewOrderBook("BTC-USD")

	// Add a buy order
	buyOrder := Order{
		ID:    "BUY-001",
		Side:  Buy,
		Price: decimal.NewFromFloat(50000),
		Qty:   decimal.NewFromFloat(1.0),
		Time:  time.Now().Unix(),
	}
	fillCh := make(chan OrderFill, 10)
	tradeCh := make(chan Trade, 10)
	book.Match(buyOrder, tradeCh, fillCh, buyOrder.Qty)
	time.Sleep(50 * time.Millisecond)

	// Clear the channels
	for len(fillCh) > 0 {
		<-fillCh
	}
	for len(tradeCh) > 0 {
		<-tradeCh
	}

	// Add a sell order that fully matches the buy order
	sellOrder := Order{
		ID:    "SELL-001",
		Side:  Sell,
		Price: decimal.NewFromFloat(49500),
		Qty:   decimal.NewFromFloat(1.0),
		Time:  time.Now().Unix(),
	}
	fillCh = make(chan OrderFill, 10)
	tradeCh = make(chan Trade, 10)
	book.Match(sellOrder, tradeCh, fillCh, sellOrder.Qty)
	time.Sleep(50 * time.Millisecond)

	// Attempt to cancel the fully filled order
	cancelFillCh := make(chan OrderFill, 1)
	success := book.Cancel("BUY-001", cancelFillCh)

	if success {
		t.Fatal("Cancel should have failed for fully filled order")
	}
}

// TestCancelNonexistentOrder tests that cancelling a non-existent order fails
func TestCancelNonexistentOrder(t *testing.T) {
	book := NewOrderBook("BTC-USD")
	fillCh := make(chan OrderFill, 1)

	success := book.Cancel("NONEXISTENT", fillCh)

	if success {
		t.Fatal("Cancel should have failed for non-existent order")
	}
}

// TestCancelRemovesOrderFromBook tests that cancelled orders are removed from the order book
func TestCancelRemovesOrderFromBook(t *testing.T) {
	book := NewOrderBook("BTC-USD")

	order := Order{
		ID:    "ORDER-001",
		Side:  Buy,
		Price: decimal.NewFromFloat(50000),
		Qty:   decimal.NewFromFloat(1.0),
		Time:  time.Now().Unix(),
	}
	fillCh := make(chan OrderFill, 10)
	tradeCh := make(chan Trade, 10)

	// Add the order
	book.Match(order, tradeCh, fillCh, order.Qty)
	time.Sleep(50 * time.Millisecond)

	// Verify the order is in the book
	if book.GetOrderCount() != 1 {
		t.Errorf("Expected 1 order in book, got %d", book.GetOrderCount())
	}

	// Cancel the order
	cancelFillCh := make(chan OrderFill, 1)
	book.Cancel("ORDER-001", cancelFillCh)
	<-cancelFillCh

	// Verify the order was removed
	if book.GetOrderCount() != 0 {
		t.Errorf("Expected 0 orders in book after cancel, got %d", book.GetOrderCount())
	}
}

// TestOriginalQtyPreservedThroughMatching tests that OriginalQty is preserved correctly
func TestOriginalQtyPreservedThroughMatching(t *testing.T) {
	book := NewOrderBook("BTC-USD")

	originalQty := decimal.NewFromFloat(5.0)
	order := Order{
		ID:    "ORDER-001",
		Side:  Buy,
		Price: decimal.NewFromFloat(50000),
		Qty:   originalQty,
		Time:  time.Now().Unix(),
	}

	// Add the order (this should set OriginalQty)
	fillCh := make(chan OrderFill, 10)
	tradeCh := make(chan Trade, 10)
	book.Match(order, tradeCh, fillCh, originalQty)
	time.Sleep(50 * time.Millisecond)

	// Retrieve the order from the orders map
	retrievedOrder := book.orders["ORDER-001"]
	if retrievedOrder == nil {
		t.Fatal("Order not found in orders map")
	}

	// Verify OriginalQty is set correctly
	if !retrievedOrder.OriginalQty.Equal(originalQty) {
		t.Errorf("Expected OriginalQty %s, got %s", originalQty, retrievedOrder.OriginalQty)
	}

	// Verify Qty is still the original amount (no matches yet)
	if !retrievedOrder.Qty.Equal(originalQty) {
		t.Errorf("Expected Qty %s, got %s", originalQty, retrievedOrder.Qty)
	}
}
