// 快速排序示例脚本：go run scripts/quicksort.go
package main

import (
	"fmt"
	"math/rand"
)

// QuickSort 对切片原地升序排序
func QuickSort(nums []int) {
	if len(nums) < 2 {
		return
	}
	quickSort(nums, 0, len(nums)-1)
}

func quickSort(nums []int, low, high int) {
	for low < high {
		p := partition(nums, low, high)
		// 先递归较短的区间，降低递归深度
		if p-low < high-p {
			quickSort(nums, low, p-1)
			low = p + 1
		} else {
			quickSort(nums, p+1, high)
			high = p - 1
		}
	}
}

// partition 以随机元素为基准，将小于基准的元素移到左侧
func partition(nums []int, low, high int) int {
	pivotIdx := low + rand.Intn(high-low+1)
	nums[pivotIdx], nums[high] = nums[high], nums[pivotIdx]
	pivot := nums[high]

	i := low
	for j := low; j < high; j++ {
		if nums[j] < pivot {
			nums[i], nums[j] = nums[j], nums[i]
			i++
		}
	}
	nums[i], nums[high] = nums[high], nums[i]
	return i
}

func main() {
	cases := [][]int{
		{5, 2, 9, 1, 5, 6},
		{3},
		{},
		{1, 2, 3, 4, 5},
		{5, 4, 3, 2, 1},
		{-3, 0, -1, 7, 2},
	}

	for _, c := range cases {
		origin := append([]int(nil), c...)
		QuickSort(c)
		fmt.Printf("排序前: %v\n排序后: %v\n\n", origin, c)
	}
}
