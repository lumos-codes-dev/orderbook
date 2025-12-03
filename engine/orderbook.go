// Package engine provides an order matching engine with order book functionality.
// It implements a price-time priority matching algorithm using heap-based data structures
// for efficient order management and execution.
package engine

import (
	"container/heap"
	"sync"
	"time"

	"github.com/shopspring/decimal"
)

// orderHeap is a slice of Order pointers that implements heap.Interface.
// It serves as the base type for both bid and ask heaps.
type orderHeap []*Order

// Len returns the number of orders in the heap.
func (h orderHeap) Len() int {
	return len(h)
}

// Swap exchanges the orders at positions i and j in the heap.
func (h orderHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
}

// Push adds a new order to the heap. The order must be of type *Order.
func (h *orderHeap) Push(x interface{}) {
	*h = append(*h, x.(*Order))
}

// Pop removes and returns the last order from the heap.
func (h *orderHeap) Pop() interface{} {
	n := len(*h)
	x := (*h)[n-1]
	*h = (*h)[:n-1]
	return x
}

// bidHeap implements a max-heap for buy orders, prioritizing higher prices.
// Orders with higher prices have higher priority in the matching process.
type bidHeap struct{ orderHeap }

// Less determines the ordering of buy orders in the heap.
// Returns true if order i has higher priority than order j (higher price).
func (h bidHeap) Less(i, j int) bool {
	return h.orderHeap[i].Price.GreaterThan(h.orderHeap[j].Price)
}

// askHeap implements a min-heap for sell orders, prioritizing lower prices.
// Orders with lower prices have higher priority in the matching process.
type askHeap struct{ orderHeap }

// Less determines the ordering of sell orders in the heap.
// Returns true if order i has higher priority than order j (lower price).
func (h askHeap) Less(i, j int) bool {
	return h.orderHeap[i].Price.LessThan(h.orderHeap[j].Price)
}

// OrderBook represents a trading pair's order book with separate bid and ask sides.
// It maintains orders in price-time priority using heap data structures for efficient
// matching and provides methods for order execution and market data retrieval.
type OrderBook struct {
	Pair   string            // Trading pair identifier (e.g., "BTC-USD")
	bids   *bidHeap          // Buy orders heap (max-heap by price)
	asks   *askHeap          // Sell orders heap (min-heap by price)
	orders map[string]*Order // map of orders
	mutex  sync.Mutex        // Protects concurrent access to the order book
}

// NewOrderBook creates and initializes a new order book for the specified trading pair.
// The returned order book has empty bid and ask heaps ready for order processing.
func NewOrderBook(pair string) *OrderBook {
	b := &bidHeap{}
	a := &askHeap{}
	heap.Init(b)
	heap.Init(a)
	return &OrderBook{
		Pair:   pair,
		bids:   b,
		asks:   a,
		orders: make(map[string]*Order),
	}
}

// Match processes an incoming order against the order book, executing trades when possible.
// It implements a price-time priority matching algorithm and sends trade and fill events
// through the provided channels.
//
// Parameters:
//   - order: The incoming order to match
//   - tradeCh: Channel to send Trade events when orders are matched
//   - fillCh: Channel to send OrderFill events for order status updates
//   - originalQty: The original quantity of the incoming order before any modifications
//
// The method handles both buy and sell orders:
//   - Buy orders match against ask orders (sells) starting from the lowest price
//   - Sell orders match against bid orders (buys) starting from the highest price
//
// If the order cannot be fully matched, the remaining quantity is added to the appropriate
// side of the order book. Fill events are sent for both the incoming order and any
// matched orders to track execution status.
func (ob *OrderBook) Match(order Order, tradeCh chan<- Trade, fillCh chan<- OrderFill, originalQty decimal.Decimal) {
	ob.mutex.Lock()
	defer ob.mutex.Unlock()

	// Set the original quantity on the order
	order.OriginalQty = originalQty

	// Track the order in the orders map
	ob.orders[order.ID] = &order

	now := time.Now().Unix()
	incomingExecutedQty := decimal.Zero

	if order.Side == Buy {
		// Loop through asks to match the incoming BUY order
		for ob.asks.Len() > 0 && !order.Qty.IsZero() {
			// Take the best-priced ask (lowest price)
			top := heap.Pop(ob.asks).(*Order)

			// If the best ask price is higher than the buy price, stop matching
			if top.Price.GreaterThan(order.Price) {
				heap.Push(ob.asks, top)
				break
			}

			// Determine how much quantity can be executed in this trade
			qty := min(order.Qty, top.Qty)
			if qty.IsZero() {
				continue
			}

			// Create a trade for matched quantity
			tradeCh <- Trade{
				Pair:         ob.Pair,
				BuyOrderID:   order.ID,
				SellOrderID:  top.ID,
				Price:        top.Price,
				Qty:          qty,
				TakerOrderID: order.ID,
				MakerOrderID: top.ID,
				TakerSide:    order.Side,
			}

			// Update remaining quantities for both orders
			order.Qty = order.Qty.Sub(qty)
			top.Qty = top.Qty.Sub(qty)
			incomingExecutedQty = incomingExecutedQty.Add(qty)

			// Determine status of the matched SELL order
			topStatus := PartiallyFilled
			if top.Qty.IsZero() {
				topStatus = Filled
			}

			// Determine status of the incoming BUY order
			orderStatus := PartiallyFilled
			if order.Qty.IsZero() {
				orderStatus = Filled
			}

			// write fill event for the matched SELL order (top of the ask heap)
			fillCh <- OrderFill{
				OrderID:      top.ID,
				Pair:         ob.Pair,
				Side:         top.Side,
				OriginalQty:  top.OriginalQty,
				ExecutedQty:  qty,
				RemainingQty: top.Qty,
				Price:        top.Price,
				FillPrice:    top.Price,
				Status:       topStatus,
				Timestamp:    now,
			}

			// write fill event for the incoming BUY order
			fillCh <- OrderFill{
				OrderID:      order.ID,
				Pair:         ob.Pair,
				Side:         order.Side,
				OriginalQty:  order.OriginalQty,
				ExecutedQty:  qty,
				RemainingQty: order.Qty,
				Price:        top.Price,
				FillPrice:    top.Price,
				Status:       orderStatus,
				Timestamp:    now,
			}

			// If the SELL order still has remaining quantity, push it back to heap
			if !top.Qty.IsZero() {
				heap.Push(ob.asks, top)
			} else {
				// Remove fully filled order from the orders map
				delete(ob.orders, top.ID)
			}
		}

		// If BUY order is not fully filled, push the remaining quantity into bids heap
		if !order.Qty.IsZero() {
			heap.Push(ob.bids, &order)
		} else {
			// Remove fully filled order from the orders map
			delete(ob.orders, order.ID)
		}
	} else {
		// Loop through bids to match the incoming SELL order
		for ob.bids.Len() > 0 && !order.Qty.IsZero() {
			// Take the best-priced bid (highest price)
			top := heap.Pop(ob.bids).(*Order)

			// If the best bid price is lower than the sell price, stop matching
			if top.Price.LessThan(order.Price) {
				heap.Push(ob.bids, top) // push it back since it can't be matched
				break
			}

			// Determine how much quantity can be executed in this trade
			qty := min(order.Qty, top.Qty)
			if qty.IsZero() {
				continue // skip if no executable quantity
			}

			// Create a trade for matched quantity
			tradeCh <- Trade{
				Pair:         ob.Pair,
				BuyOrderID:   top.ID,
				SellOrderID:  order.ID,
				Price:        top.Price,
				Qty:          qty,
				TakerOrderID: order.ID,
				MakerOrderID: top.ID,
				TakerSide:    order.Side,
			}

			// Update remaining quantities for both orders
			order.Qty = order.Qty.Sub(qty)
			top.Qty = top.Qty.Sub(qty)
			incomingExecutedQty = incomingExecutedQty.Add(qty)

			// Determine status of the matched BUY order
			topStatus := PartiallyFilled
			if top.Qty.IsZero() {
				topStatus = Filled
			}

			// Determine status of the incoming SELL order
			orderStatus := PartiallyFilled
			if order.Qty.IsZero() {
				orderStatus = Filled
			}

			// write fill event for the incoming BUY order
			fillCh <- OrderFill{
				OrderID:      top.ID,
				Pair:         ob.Pair,
				Side:         top.Side,
				OriginalQty:  top.OriginalQty,
				ExecutedQty:  qty,
				RemainingQty: top.Qty,
				Price:        top.Price,
				FillPrice:    top.Price,
				Status:       topStatus,
				Timestamp:    now,
			}

			// write fill event for the matched SELL order (top of the ask heap)
			fillCh <- OrderFill{
				OrderID:      order.ID,
				Pair:         ob.Pair,
				Side:         order.Side,
				OriginalQty:  order.OriginalQty,
				ExecutedQty:  qty,
				RemainingQty: order.Qty,
				Price:        top.Price,
				FillPrice:    top.Price,
				Status:       orderStatus,
				Timestamp:    now,
			}

			// If the BUY order still has remaining quantity, push it back to heap
			if !top.Qty.IsZero() {
				heap.Push(ob.bids, top)
			} else {
				// Remove fully filled order from the orders map
				delete(ob.orders, top.ID)
			}
		}

		// If SELL order is not fully filled, push the remaining quantity into asks heap
		if !order.Qty.IsZero() {
			heap.Push(ob.asks, &order)
		} else {
			// Remove fully filled order from the orders map
			delete(ob.orders, order.ID)
		}
	}

	// If no quantity was executed at all, mark the order as NEW
	if order.Qty.Equal(originalQty) {
		fillCh <- OrderFill{
			OrderID:      order.ID,
			Pair:         ob.Pair,
			Side:         order.Side,
			OriginalQty:  originalQty,
			ExecutedQty:  decimal.Zero,
			RemainingQty: order.Qty,
			Price:        order.Price,
			FillPrice:    decimal.Zero,
			Status:       New,
			Timestamp:    now,
		}
	}

	// closing channels in place where it gets written to prevent panic: write on closed channel
	close(tradeCh)
	close(fillCh)
}

func (ob *OrderBook) Cancel(orderId string, fillCh chan<- OrderFill) bool {
	ob.mutex.Lock()
	defer ob.mutex.Unlock()

	order, exists := ob.orders[orderId]
	if !exists {
		return false
	}

	// order is already filled
	if order.Qty.IsZero() {
		return false
	}

	// delete from heap
	switch order.Side {
	case Buy:
		ob.removeBidOrder(orderId)
	case Sell:
		ob.removeAskOrder(orderId)
	}

	now := time.Now().Unix()

	// Calculate the executed quantity
	// ExecutedQty = OriginalQty - CurrentQty(Qty)
	executedQty := order.OriginalQty.Sub(order.Qty)

	// send new fill uodate as a cancelled

	delete(ob.orders, orderId)

	fillCh <- OrderFill{
		OrderID:      orderId,
		Pair:         ob.Pair,
		Side:         order.Side,
		OriginalQty:  order.OriginalQty,
		ExecutedQty:  executedQty,
		RemainingQty: decimal.Zero, // Cancelled portion is no longer on book
		Price:        order.Price,
		FillPrice:    decimal.Zero,
		Status:       Cancelled,
		Timestamp:    now,
	}

	// closing channels in place where it gets written to prevent panic: write on closed channel
	close(fillCh)

	return true
}

// BestBid returns the highest bid price in the order book.
// Returns 0 if there are no bid orders.
func (ob *OrderBook) BestBid() float64 {
	ob.mutex.Lock()
	defer ob.mutex.Unlock()

	if ob.bids.Len() == 0 {
		return 0
	}
	return ob.bids.orderHeap[0].Price.InexactFloat64()
}

// BestAsk returns the lowest ask price in the order book.
// Returns 0 if there are no ask orders.
func (ob *OrderBook) BestAsk() float64 {
	ob.mutex.Lock()
	defer ob.mutex.Unlock()

	if ob.asks.Len() == 0 {
		return 0
	}
	return ob.asks.orderHeap[0].Price.InexactFloat64()
}

// GetBidDepth returns the bid side market depth up to the specified number of price levels.
// Each DepthLevel contains the aggregated quantity and trade count for orders at that price.
// The levels are ordered from highest to lowest price (best to worst for buyers).
//
// Parameters:
//   - depth: Maximum number of price levels to return
//
// Returns an empty slice if depth <= 0 or there are no bid orders.
func (ob *OrderBook) GetBidDepth(depth int) []DepthLevel {
	ob.mutex.Lock()
	defer ob.mutex.Unlock()

	if depth <= 0 || ob.bids.Len() == 0 {
		return []DepthLevel{}
	}

	priceMap := make(map[string]decimal.Decimal)
	countMap := make(map[string]int)

	for _, order := range ob.bids.orderHeap {
		priceKey := order.Price.String()
		priceMap[priceKey] = priceMap[priceKey].Add(order.Qty)
		countMap[priceKey]++
	}

	var levels []DepthLevel
	processedPrices := make(map[string]bool)

	for _, order := range ob.bids.orderHeap {
		priceKey := order.Price.String()
		if processedPrices[priceKey] {
			continue
		}

		levels = append(levels, DepthLevel{
			Price:      order.Price,
			Quantity:   priceMap[priceKey],
			TradeCount: countMap[priceKey],
		})
		processedPrices[priceKey] = true

		if len(levels) >= depth {
			break
		}
	}

	return levels
}

// GetAskDepth returns the ask side market depth up to the specified number of price levels.
// Each DepthLevel contains the aggregated quantity and trade count for orders at that price.
// The levels are ordered from lowest to highest price (best to worst for sellers).
//
// Parameters:
//   - depth: Maximum number of price levels to return
//
// Returns an empty slice if depth <= 0 or there are no ask orders.
func (ob *OrderBook) GetAskDepth(depth int) []DepthLevel {
	ob.mutex.Lock()
	defer ob.mutex.Unlock()

	if depth <= 0 || ob.asks.Len() == 0 {
		return []DepthLevel{}
	}

	priceMap := make(map[string]decimal.Decimal)
	countMap := make(map[string]int)

	for _, order := range ob.asks.orderHeap {
		priceKey := order.Price.String()
		priceMap[priceKey] = priceMap[priceKey].Add(order.Qty)
		countMap[priceKey]++
	}

	var levels []DepthLevel
	processedPrices := make(map[string]bool)

	for _, order := range ob.asks.orderHeap {
		priceKey := order.Price.String()
		if processedPrices[priceKey] {
			continue
		}

		levels = append(levels, DepthLevel{
			Price:      order.Price,
			Quantity:   priceMap[priceKey],
			TradeCount: countMap[priceKey],
		})
		processedPrices[priceKey] = true

		if len(levels) >= depth {
			break
		}
	}

	return levels
}

func (ob *OrderBook) removeBidOrder(orderId string) {
	for i := 0; i < len(ob.bids.orderHeap); i++ {
		if ob.bids.orderHeap[i].ID == orderId {
			heap.Remove(ob.bids, i)
		}
	}
}
func (ob *OrderBook) removeAskOrder(orderId string) {
	for i := 0; i < len(ob.asks.orderHeap); i++ {
		if ob.asks.orderHeap[i].ID == orderId {
			heap.Remove(ob.asks, i)
		}
	}
}

// min returns the smaller of two decimal values.
func min(a, b decimal.Decimal) decimal.Decimal {
	if a.LessThan(b) {
		return a
	}
	return b
}

// GetOrderCount returns the number of active orders currently in the order book.
// This includes both partially filled and new (unfilled) orders.
// Fully filled and cancelled orders are not included.
func (ob *OrderBook) GetOrderCount() int {
	ob.mutex.Lock()
	defer ob.mutex.Unlock()
	return len(ob.orders)
}
