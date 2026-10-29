// 冒泡排序示例脚本：go run scripts/bubblesort.go
package main

import "fmt"

// BubbleSort 对切片原地升序排序
func BubbleSort(nums []int) {
	n := len(nums)
	for i := 0; i < n-1; i++ {
		swapped := false
		// 每轮结束，末尾 i+1 个元素已就位
		for j := 0; j < n-1-i; j++ {
			if nums[j] > nums[j+1] {
				nums[j], nums[j+1] = nums[j+1], nums[j]
				swapped = true
			}
		}
		// 本轮无交换说明已有序，提前结束
		if !swapped {
			break
		}
	}
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
		BubbleSort(c)
		fmt.Printf("排序前: %v\n排序后: %v\n\n", origin, c)
	}
}
