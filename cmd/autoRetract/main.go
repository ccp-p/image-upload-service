package main

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type Group struct {
	Name        string `json:"name"`
	Link        string `json:"link"`
	Members     int    `json:"members"`
	Description string `json:"description"`
}

func main() {
	content, err := os.ReadFile(`C:\Users\83795\Desktop\1.html`)
	if err != nil {
		fmt.Printf("Error reading file: %v\n", err)
		return
	}

	html := string(content)

	// Regex to find groups
	// Pattern: <strong># Name</strong></p>\s*<ul class="simple">\s*<li><p>链接: <a ... href="LINK">...</a> - MEMBERS 位成员\s*(DESCRIPTION)</p></li>\s*</ul>
	re := regexp.MustCompile(`(?s)<p><strong>#\s*(.*?)</strong></p>\s*<ul class="simple">\s*<li><p>链接:\s*<a[^>]*href="(.*?)">.*?</a>\s*-\s*([\d\s,]+)\s*位成员\s*(.*?)</p></li>\s*</ul>`)
	matches := re.FindAllStringSubmatch(html, -1)

	var groups []Group
	for _, match := range matches {
		name := strings.TrimSpace(match[1])
		link := strings.TrimSpace(match[2])
		memberStr := strings.ReplaceAll(match[3], " ", "") // Remove potential spaces in numbers like "92 3"
		memberStr = strings.ReplaceAll(memberStr, ",", "")
		members, _ := strconv.Atoi(memberStr)
		description := strings.TrimSpace(match[4])
		// Remove HTML tags from description
		description = regexp.MustCompile(`<[^>]*>`).ReplaceAllString(description, "")

		groups = append(groups, Group{
			Name:        name,
			Link:        link,
			Members:     members,
			Description: description,
		})
	}

	// Sort by members descending
	sort.Slice(groups, func(i, j int) bool {
		return groups[i].Members > groups[j].Members
	})

	// Save to JSON for the web interface
	jsonData, _ := json.MarshalIndent(groups, "", "  ")

	// Create a beautiful HTML index
	htmlTemplate := `
<!DOCTYPE html>
<html lang="zh-CN">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Telegram 群组排名</title>
    <script src="https://cdn.tailwindcss.com"></script>
    <style>
        .group-card { transition: transform 0.2s; }
        .group-card:hover { transform: translateY(-2px); }
    </style>
</head>
<body class="bg-gray-50 text-gray-900 font-sans">
    <div class="container mx-auto px-4 py-8 max-w-5xl">
        <header class="mb-12 text-center">
            <h1 class="text-4xl font-extrabold text-blue-600 mb-2">Telegram 群组检索</h1>
            <p class="text-gray-500">根据成员人数降序排列</p>
        </header>

        <div id="app">
            <div id="list" class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-6">
                <!-- Data will be injected here -->
            </div>

            <div class="mt-12 flex justify-center items-center space-x-4" id="pagination">
                <button id="prevBtn" class="px-6 py-2 bg-white border border-gray-300 rounded-full hover:bg-gray-100 disabled:opacity-30 disabled:cursor-not-allowed">上一页</button>
                <span id="pageInfo" class="font-medium text-gray-700 text-lg">第 1 / 1 页</span>
                <button id="nextBtn" class="px-6 py-2 bg-white border border-gray-300 rounded-full hover:bg-gray-100 disabled:opacity-30 disabled:cursor-not-allowed">下一页</button>
            </div>
        </div>
    </div>

    <script>
        const groups = %s;
        const itemsPerPage = 30;
        let currentPage = 1;

        function renderPage(page) {
            const start = (page - 1) * itemsPerPage;
            const end = start + itemsPerPage;
            const paginatedItems = groups.slice(start, end);
            const totalPages = Math.ceil(groups.length / itemsPerPage);

            const listEl = document.getElementById('list');
            listEl.innerHTML = paginatedItems.map((g, index) => {
                const rank = start + index + 1;
                return ` + "`" + `
                <div class="group-card bg-white p-6 rounded-2xl shadow-sm border border-gray-100 flex flex-col h-full">
                    <div class="flex items-start justify-between mb-4">
                        <span class="text-xs font-bold text-blue-500 bg-blue-50 px-2 py-1 rounded">Rank #${rank}</span>
                        <span class="text-sm font-semibold text-orange-600 bg-orange-50 px-3 py-1 rounded-full">${g.members.toLocaleString()} 成员</span>
                    </div>
                    <h3 class="text-xl font-bold mb-2 break-words text-gray-800">${g.name}</h3>
                    <p class="text-gray-500 text-sm mb-6 flex-grow overflow-hidden line-clamp-3">${g.description || '暂无描述'}</p>
                    <a href="${g.link}" target="_blank" class="w-full text-center py-3 bg-blue-600 text-white rounded-xl font-bold hover:bg-blue-700 transition-colors">立即加入</a>
                </div>
            ` + "`" + `;
            }).join('');

            document.getElementById('pageInfo').innerText = ` + "`" + `第 ${page} / ${totalPages} 页` + "`" + `;
            document.getElementById('prevBtn').disabled = page === 1;
            document.getElementById('nextBtn').disabled = (page === totalPages || totalPages === 0);
            window.scrollTo({ top: 0, behavior: 'smooth' });

            document.getElementById('prevBtn').onclick = () => {
                if (currentPage > 1) {
                    currentPage--;
                    renderPage(currentPage);
                }
            };

            document.getElementById('nextBtn').onclick = () => {
                const totalPages = Math.ceil(groups.length / itemsPerPage);
                if (currentPage < totalPages) {
                    currentPage++;
                    renderPage(currentPage);
                }
            };
        }

        renderPage(1);
    </script>
</body>
</html>
`
	finalHtml := fmt.Sprintf(htmlTemplate, string(jsonData))
	os.WriteFile("cmd/autoRetract/index.html", []byte(finalHtml), 0644)
	fmt.Printf("Processing complete. Found %d groups. Output saved to cmd/autoRetract/index.html\n", len(groups))
}

